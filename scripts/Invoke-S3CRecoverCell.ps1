[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SourceCellPath,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [Parameter(Mandatory = $true)][string]$RecoveryId
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Utf8 = New-Object Text.UTF8Encoding($false, $true)
$script:PathComparison = [StringComparison]::OrdinalIgnoreCase
$script:FailureCode = 'UNCLASSIFIED'
$script:FailureExceptionType = ''
$script:RecoveryCreated = $false
$script:RecoveryPath = $null
$script:OperationalTimeoutMilliseconds = 1800000
$script:CaptureDrainMilliseconds = 30000
$script:MaxJsonBytes = 16777216

function Stop-S3CRecovery {
    param([Parameter(Mandatory = $true)][string]$Code)
    $script:FailureCode = $Code
    throw 'S3C_RECOVERY_STOP'
}

function Get-S3CRecoveryFullPath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Stop-S3CRecovery -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Stop-S3CRecovery -Code $Code
        }
    }
    try {
        return [IO.Path]::GetFullPath($Value)
    } catch {
        Stop-S3CRecovery -Code $Code
    }
}

function Test-S3CRecoveryPathContains {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate,
        [switch]$AllowEqual
    )
    if ([string]::Equals($Parent, $Candidate, $script:PathComparison)) {
        return [bool]$AllowEqual
    }
    $prefix = $Parent.TrimEnd([char]92, [char]47) +
        [IO.Path]::DirectorySeparatorChar
    return $Candidate.StartsWith($prefix, $script:PathComparison)
}

function Assert-S3CRecoveryNoReparseAncestry {
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
                Stop-S3CRecovery -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Stop-S3CRecovery -Code $Code
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

function Assert-S3CRecoveryDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        Stop-S3CRecovery -Code $Code
    }
    Assert-S3CRecoveryNoReparseAncestry -Path $Path -Code $Code
    $item = Get-Item -LiteralPath $Path -Force
    if (-not ($item -is [IO.DirectoryInfo])) {
        Stop-S3CRecovery -Code $Code
    }
}

function ConvertTo-S3CRecoveryRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $prefix = $Root.TrimEnd([char]92, [char]47) +
        [IO.Path]::DirectorySeparatorChar
    if (-not $Path.StartsWith($prefix, $script:PathComparison)) {
        Stop-S3CRecovery -Code $Code
    }
    return $Path.Substring($prefix.Length).Replace('\', '/')
}

function Resolve-S3CRecoveryCellRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$CellRoot,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath.IndexOf(':') -ge 0) {
        Stop-S3CRecovery -Code $Code
    }
    $segments = $RelativePath.Replace('/', '\').Split([char]92)
    foreach ($segment in $segments) {
        if ([string]::IsNullOrWhiteSpace($segment) -or
            $segment -ceq '.' -or $segment -ceq '..') {
            Stop-S3CRecovery -Code $Code
        }
    }
    try {
        $resolved = [IO.Path]::GetFullPath((Join-Path $CellRoot $RelativePath))
    } catch {
        Stop-S3CRecovery -Code $Code
    }
    if (-not (Test-S3CRecoveryPathContains `
        -Parent $CellRoot `
        -Candidate $resolved)) {
        Stop-S3CRecovery -Code $Code
    }
    Assert-S3CRecoveryNoReparseAncestry -Path $resolved -Code $Code
    return $resolved
}

function Get-S3CRecoverySafeTreeFiles {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-S3CRecoveryDirectory -Path $Root -Code $Code
    $stack = New-Object 'Collections.Generic.Stack[string]'
    $stack.Push($Root)
    while ($stack.Count -gt 0) {
        $directory = $stack.Pop()
        try {
            $children = (New-Object IO.DirectoryInfo($directory)).
                EnumerateFileSystemInfos()
        } catch {
            Stop-S3CRecovery -Code $Code
        }
        foreach ($child in $children) {
            if (($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Stop-S3CRecovery -Code $Code
            }
            if ($child -is [IO.DirectoryInfo]) {
                $stack.Push($child.FullName)
            } elseif ($child -is [IO.FileInfo]) {
                Write-Output $child
            } else {
                Stop-S3CRecovery -Code $Code
            }
        }
    }
}

function Get-S3CRecoveryFileLock {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-S3CRecoveryNoReparseAncestry -Path $Path -Code $Code
    try {
        $identity = [S3CRecoveryFile]::HashNoFollow($Path)
    } catch {
        Stop-S3CRecovery -Code $Code
    }
    return [pscustomobject]@{
        SizeBytes = [int64]$identity.SizeBytes
        Sha256 = [string]$identity.Sha256
    }
}

function Read-S3CRecoveryJsonObject {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-S3CRecoveryNoReparseAncestry -Path $Path -Code $Code
    try {
        [byte[]]$bytes = [S3CRecoveryFile]::ReadNoFollow(
            $Path,
            $script:MaxJsonBytes
        )
        if ($bytes.Length -eq 0) { throw 'empty JSON' }
        $text = $script:Utf8.GetString($bytes)
        $value = $text | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Stop-S3CRecovery -Code $Code
    }
    if ($null -eq $value -or -not ($value -is [pscustomobject])) {
        Stop-S3CRecovery -Code $Code
    }
    return $value
}

function Copy-S3CRecoveryNoFollow {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $parent = [IO.Path]::GetDirectoryName($Destination)
    try {
        [void][IO.Directory]::CreateDirectory($parent)
    } catch {
        Stop-S3CRecovery -Code $Code
    }
    Assert-S3CRecoveryNoReparseAncestry -Path $Source -Code $Code
    Assert-S3CRecoveryNoReparseAncestry -Path $parent -Code $Code
    try {
        $identity = [S3CRecoveryFile]::CopyNoFollow($Source, $Destination)
    } catch {
        Stop-S3CRecovery -Code $Code
    }
    return [pscustomobject]@{
        SizeBytes = [int64]$identity.SizeBytes
        Sha256 = [string]$identity.Sha256
    }
}

function ConvertTo-S3CRecoveryWindowsArgument {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') {
        return $Value
    }
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq [char]92) {
            $slashes++
            continue
        }
        if ($character -eq [char]34) {
            [void]$builder.Append(('\' * (($slashes * 2) + 1)))
            [void]$builder.Append('"')
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) {
            [void]$builder.Append(('\' * $slashes))
            $slashes = 0
        }
        [void]$builder.Append($character)
    }
    if ($slashes -gt 0) {
        [void]$builder.Append(('\' * ($slashes * 2)))
    }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function Join-S3CRecoveryWindowsArguments {
    param([Parameter(Mandatory = $true)][string[]]$Values)
    return (($Values | ForEach-Object {
        ConvertTo-S3CRecoveryWindowsArgument -Value ([string]$_)
    }) -join ' ')
}

function Test-S3CRecoveryCredentialCandidate {
    param([Parameter(Mandatory = $true)][string]$Value)
    if ($Value.Length -lt 16 -or $Value.Length -gt 512) { return $false }
    $normalized = $Value.ToLowerInvariant().Trim([char[]]'.-_~')
    if ($normalized -match '^(?:redact|mask|omit|placeholder|example|dummy)' -or
        $normalized -match '^your[-_]' -or
        $normalized -match '^(?:token|secret|api[-_]?key)(?:[-_]?here)?$' -or
        $normalized -match '^(?:x|0){8,}$') {
        return $false
    }
    $unique = New-Object 'Collections.Generic.HashSet[char]'
    foreach ($character in $Value.ToCharArray()) {
        [void]$unique.Add($character)
    }
    if ($unique.Count -lt 8) { return $false }
    return [bool](
        $Value -match '[0-9._~+/\-]' -or
        ($Value -cmatch '[A-Z]' -and $Value -cmatch '[a-z]') -or
        ($Value.Length -ge 32 -and $unique.Count -ge 16)
    )
}

function Test-S3CRecoveryCaptureLeak {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes
    )
    $texts = New-Object 'Collections.Generic.List[string]'
    foreach ($encoding in @(
        $script:Utf8,
        (New-Object Text.UnicodeEncoding($false, $false, $true)),
        (New-Object Text.UnicodeEncoding($true, $false, $true))
    )) {
        try { $texts.Add($encoding.GetString($Bytes)) } catch {}
    }
    $patterns = @(
        '(?i)(?:^|[^A-Za-z0-9])Bearer[ \t]+(?<credential>[A-Za-z0-9][A-Za-z0-9._~+/\-]{15,511})',
        '(?i)Authorization(?:\\?")?[ \t]*(?::|=)[ \t]*(?:\\?")?[ \t]*(?:(?:Bearer|Basic|Token)[ \t]+)?(?<credential>[A-Za-z0-9][A-Za-z0-9._~+/\-]{15,511})'
    )
    foreach ($text in $texts) {
        foreach ($pattern in $patterns) {
            foreach ($match in [Text.RegularExpressions.Regex]::Matches(
                $text,
                $pattern,
                [Text.RegularExpressions.RegexOptions]::CultureInvariant
            )) {
                if (Test-S3CRecoveryCredentialCandidate `
                    -Value $match.Groups['credential'].Value) {
                    return $true
                }
            }
        }
    }
    return $false
}

function Get-S3CRecoveryBackupPayload {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$ExpectedBundle
    )
    if ($Bytes.Length -eq 0 -or $Bytes.Length -gt $script:MaxJsonBytes) {
        return $null
    }
    try {
        $text = $script:Utf8.GetString($Bytes)
        $value = $text | ConvertFrom-Json -ErrorAction Stop
    } catch {
        return $null
    }
    if ($null -eq $value -or -not ($value -is [pscustomobject])) {
        return $null
    }
    $rootNames = @($value.PSObject.Properties | ForEach-Object { $_.Name })
    if ($rootNames.Count -ne 2 -or
        -not ($rootNames -ccontains 'bundle') -or
        -not ($rootNames -ccontains 'manifest')) {
        return $null
    }
    $bundleProperty = $value.PSObject.Properties['bundle']
    $manifestProperty = $value.PSObject.Properties['manifest']
    if ($null -eq $bundleProperty -or
        -not ($bundleProperty.Value -is [string]) -or
        $null -eq $manifestProperty -or
        -not ($manifestProperty.Value -is [pscustomobject])) {
        return $null
    }
    try {
        $reportedBundle = [IO.Path]::GetFullPath([string]$bundleProperty.Value)
    } catch {
        return $null
    }
    if (-not [string]::Equals(
        $reportedBundle,
        $ExpectedBundle,
        $script:PathComparison
    )) {
        return $null
    }
    $manifest = $manifestProperty.Value
    foreach ($name in @(
        'format_version', 'created_at', 'tool_version', 'database',
        'store_identity', 'current', 'artifacts', 'artifact_count',
        'attempt_counts', 'manifest_digest'
    )) {
        if ($null -eq $manifest.PSObject.Properties[$name]) { return $null }
    }
    if ([string]$manifest.format_version -cne
        'freeagent.current-store-backup/v1' -or
        -not ($manifest.database -is [pscustomobject]) -or
        -not ($manifest.store_identity -is [pscustomobject]) -or
        [string]$manifest.database.path -cne 'database.sqlite' -or
        [string]$manifest.database.sha256 -cnotmatch '^[0-9a-f]{64}$' -or
        [int64]$manifest.database.size_bytes -le 0 -or
        [string]$manifest.manifest_digest -cnotmatch '^[0-9a-f]{64}$' -or
        [string]::IsNullOrWhiteSpace(
            [string]$manifest.store_identity.store_instance_id
        ) -or
        [int64]$manifest.artifact_count -lt 0 -or
        [int64]$manifest.artifact_count -ne @($manifest.artifacts).Count) {
        return $null
    }
    try {
        $manifestIdentity = $manifest | ConvertTo-Json -Depth 30 -Compress
    } catch {
        return $null
    }
    return [pscustomobject]@{
        Value = $value
        Manifest = $manifest
        ManifestIdentity = $manifestIdentity
        ManifestDigest = [string]$manifest.manifest_digest
        StoreInstanceId = [string]$manifest.store_identity.store_instance_id
        DatabaseSha256 = [string]$manifest.database.sha256
    }
}

function Remove-S3CRecoveryPending {
    param([AllowNull()][string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return $true }
    try {
        if ([IO.File]::Exists($Path)) { [IO.File]::Delete($Path) }
        return -not [IO.File]::Exists($Path)
    } catch {
        return $false
    }
}

function Move-S3CRecoveryCreateNew {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    try {
        [IO.File]::Move($Source, $Destination)
    } catch {
        Stop-S3CRecovery -Code 'CAPTURE_COMMIT_FAILED'
    }
}

function Invoke-S3CRecoveryPhase {
    param(
        [Parameter(Mandatory = $true)][string]$Phase,
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$ExpectedBundle,
        [Parameter(Mandatory = $true)][string]$SecretEnvironmentName
    )
    $nonce = [Guid]::NewGuid().ToString('N')
    $stdoutPending = Join-Path $script:RecoveryPath (
        ".$Phase.stdout.pending.$nonce"
    )
    $stderrPending = Join-Path $script:RecoveryPath (
        ".$Phase.stderr.pending.$nonce"
    )
    $stdoutDestination = Join-Path $script:RecoveryPath ($Phase + '.json')
    $incompleteDestination = Join-Path $script:RecoveryPath (
        $Phase + '.stdout.incomplete'
    )
    $stderrDestination = Join-Path $script:RecoveryPath (
        $Phase + '.stderr.txt'
    )
    $stdoutStream = $null
    $stderrStream = $null
    $process = $null
    $jobHandle = [IntPtr]::Zero
    $phaseFailed = $false
    $timedOut = $false
    $exitCode = 125
    try {
        $stdoutStream = New-Object IO.FileStream(
            $stdoutPending,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stderrStream = New-Object IO.FileStream(
            $stderrPending,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $Executable
        $start.Arguments = Join-S3CRecoveryWindowsArguments -Values $Arguments
        $start.WorkingDirectory = $WorkingDirectory
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        foreach ($name in @($start.EnvironmentVariables.Keys)) {
            $candidate = [string]$name
            if ([string]::Equals(
                $candidate,
                $SecretEnvironmentName,
                [StringComparison]::OrdinalIgnoreCase
            ) -or $candidate -match
                '(?i)(^|_)(API_?KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH)(_|$)') {
                $start.EnvironmentVariables.Remove($candidate)
            }
        }
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'process start failed' }
        $jobHandle = [S3CRecoveryProcessJob]::Create($process)
        $stdoutTask = $process.StandardOutput.BaseStream.CopyToAsync($stdoutStream)
        $stderrTask = $process.StandardError.BaseStream.CopyToAsync($stderrStream)
        $timedOut = -not $process.WaitForExit(
            $script:OperationalTimeoutMilliseconds
        )
        if ($timedOut) {
            [S3CRecoveryProcessJob]::Terminate($jobHandle)
            if (-not $process.WaitForExit($script:CaptureDrainMilliseconds)) {
                throw 'bounded process termination failed'
            }
        }
        [S3CRecoveryProcessJob]::Close($jobHandle)
        $jobHandle = [IntPtr]::Zero
        if (-not $stdoutTask.Wait($script:CaptureDrainMilliseconds)) {
            throw 'bounded stdout drain failed'
        }
        if (-not $stderrTask.Wait($script:CaptureDrainMilliseconds)) {
            throw 'bounded stderr drain failed'
        }
        $stdoutStream.Flush($true)
        $stderrStream.Flush($true)
        $exitCode = if ($timedOut) { 124 } else { [int]$process.ExitCode }
    } catch {
        $script:FailureExceptionType = $_.Exception.GetType().FullName
        $phaseFailed = $true
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [S3CRecoveryProcessJob]::Terminate($jobHandle) } catch {}
            try { [S3CRecoveryProcessJob]::Close($jobHandle) } catch {}
            $jobHandle = [IntPtr]::Zero
        } elseif ($null -ne $process) {
            try { if (-not $process.HasExited) { $process.Kill() } } catch {}
        }
    } finally {
        if ($null -ne $stdoutStream) { $stdoutStream.Dispose() }
        if ($null -ne $stderrStream) { $stderrStream.Dispose() }
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [S3CRecoveryProcessJob]::Close($jobHandle) } catch {}
        }
        if ($null -ne $process) { $process.Dispose() }
    }
    if ($phaseFailed) {
        $stdoutRemoved = Remove-S3CRecoveryPending -Path $stdoutPending
        $stderrRemoved = Remove-S3CRecoveryPending -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = 125
            JsonValid = $false
            Payload = $null
            TimedOut = [bool]$timedOut
            CaptureFailed = $true
            LeakDetected = $false
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    try {
        [byte[]]$stdoutBytes = [IO.File]::ReadAllBytes($stdoutPending)
        [byte[]]$stderrBytes = [IO.File]::ReadAllBytes($stderrPending)
    } catch {
        $stdoutRemoved = Remove-S3CRecoveryPending -Path $stdoutPending
        $stderrRemoved = Remove-S3CRecoveryPending -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = $exitCode
            JsonValid = $false
            Payload = $null
            TimedOut = [bool]$timedOut
            CaptureFailed = $true
            LeakDetected = $false
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    $leak = (Test-S3CRecoveryCaptureLeak -Bytes $stdoutBytes) -or
        (Test-S3CRecoveryCaptureLeak -Bytes $stderrBytes)
    if ($leak) {
        $stdoutRemoved = Remove-S3CRecoveryPending -Path $stdoutPending
        $stderrRemoved = Remove-S3CRecoveryPending -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = $exitCode
            JsonValid = $false
            Payload = $null
            TimedOut = [bool]$timedOut
            CaptureFailed = $false
            LeakDetected = $true
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    $payload = Get-S3CRecoveryBackupPayload `
        -Bytes $stdoutBytes `
        -ExpectedBundle $ExpectedBundle
    $jsonValid = $null -ne $payload
    if ($jsonValid) {
        Move-S3CRecoveryCreateNew `
            -Source $stdoutPending `
            -Destination $stdoutDestination
    } else {
        Move-S3CRecoveryCreateNew `
            -Source $stdoutPending `
            -Destination $incompleteDestination
    }
    Move-S3CRecoveryCreateNew `
        -Source $stderrPending `
        -Destination $stderrDestination
    return [pscustomobject]@{
        ExitCode = $exitCode
        JsonValid = [bool]$jsonValid
        Payload = $payload
        TimedOut = [bool]$timedOut
        CaptureFailed = $false
        LeakDetected = $false
        CleanupFailed = $false
    }
}

function Test-S3CRecoverySidecarsAbsent {
    param([Parameter(Mandatory = $true)][string]$DatabasePath)
    foreach ($suffix in @('-wal', '-shm', '-journal')) {
        $path = $DatabasePath + $suffix
        if (Test-Path -LiteralPath $path) { return $false }
    }
    return $true
}

function Test-S3CRecoverySourceUnchanged {
    param(
        [Parameter(Mandatory = $true)][object[]]$Snapshot,
        [Parameter(Mandatory = $true)][string]$ArtifactRoot,
        [Parameter(Mandatory = $true)][string]$DatabasePath
    )
    try {
        if (-not (Test-S3CRecoverySidecarsAbsent -DatabasePath $DatabasePath)) {
            return $false
        }
        $expectedArtifacts = @($Snapshot | Where-Object {
            $_.Kind -ceq 'artifact'
        } | ForEach-Object { $_.RelativePath } | Sort-Object)
        $actualArtifacts = @(
            Get-S3CRecoverySafeTreeFiles `
                -Root $ArtifactRoot `
                -Code 'SOURCE_CHANGED' |
                ForEach-Object {
                    ConvertTo-S3CRecoveryRelativePath `
                        -Root $ArtifactRoot `
                        -Path $_.FullName `
                        -Code 'SOURCE_CHANGED'
                } | Sort-Object
        )
        if (($expectedArtifacts -join "`n") -cne
            ($actualArtifacts -join "`n")) {
            return $false
        }
        foreach ($entry in $Snapshot) {
            $lock = Get-S3CRecoveryFileLock `
                -Path $entry.FullPath `
                -Code 'SOURCE_CHANGED'
            if ($lock.SizeBytes -ne $entry.SizeBytes -or
                $lock.Sha256 -cne $entry.Sha256) {
                return $false
            }
        }
        return $true
    } catch {
        return $false
    }
}

function Get-S3CRecoveryTreeLocks {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $locks = New-Object 'Collections.Generic.List[object]'
    foreach ($file in @(Get-S3CRecoverySafeTreeFiles -Root $Root -Code $Code)) {
        $lock = Get-S3CRecoveryFileLock -Path $file.FullName -Code $Code
        $locks.Add([ordered]@{
            relative_path = ConvertTo-S3CRecoveryRelativePath `
                -Root $Root `
                -Path $file.FullName `
                -Code $Code
            size_bytes = $lock.SizeBytes
            sha256 = $lock.Sha256
        })
    }
    return @($locks | Sort-Object relative_path)
}

function Write-S3CRecoveryAtomicRecord {
    param(
        [Parameter(Mandatory = $true)][string]$RecoveryPath,
        [Parameter(Mandatory = $true)]$Value
    )
    $destination = Join-Path $RecoveryPath 'recovery.json'
    $pending = Join-Path $RecoveryPath (
        '.recovery.pending.' + [Guid]::NewGuid().ToString('N')
    )
    $stream = $null
    try {
        $json = $Value | ConvertTo-Json -Depth 30 -Compress
        [byte[]]$bytes = $script:Utf8.GetBytes($json + "`n")
        $stream = New-Object IO.FileStream(
            $pending,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
        $stream.Dispose()
        $stream = $null
        [IO.File]::Move($pending, $destination)
    } catch {
        if ($null -ne $stream) { $stream.Dispose() }
        [void](Remove-S3CRecoveryPending -Path $pending)
        throw
    }
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    [Console]::Error.WriteLine('S3C_RECOVERY_FAIL code=WINDOWS_REQUIRED')
    exit 1
}

if ($null -eq ('S3CRecoveryExclusiveDirectory' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using Microsoft.Win32.SafeHandles;

public sealed class S3CRecoveryFileIdentity
{
    public long SizeBytes { get; set; }
    public string Sha256 { get; set; }
}

public static class S3CRecoveryExclusiveDirectory
{
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateDirectory(string path, IntPtr attributes);

    public static int Create(string path)
    {
        if (CreateDirectory(path, IntPtr.Zero)) return 0;
        return Marshal.GetLastWin32Error();
    }
}

public static class S3CRecoveryFile
{
    private const uint GenericRead = 0x80000000;
    private const uint ShareRead = 0x00000001;
    private const uint ShareWrite = 0x00000002;
    private const uint OpenExisting = 3;
    private const uint OpenReparsePoint = 0x00200000;
    private const uint AttributeReparsePoint = 0x00000400;
    private const uint AttributeDirectory = 0x00000010;
    private const int FileStreamInfo = 7;
    private const int ErrorInsufficientBuffer = 122;
    private const int ErrorMoreData = 234;
    private const int StreamHeaderBytes = 24;
    private const int MaximumStreamInfoBytes = 1048576;

    [StructLayout(LayoutKind.Sequential)]
    private struct ByHandleFileInformation
    {
        public uint FileAttributes;
        public System.Runtime.InteropServices.ComTypes.FILETIME CreationTime;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastAccessTime;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWriteTime;
        public uint VolumeSerialNumber;
        public uint FileSizeHigh;
        public uint FileSizeLow;
        public uint NumberOfLinks;
        public uint FileIndexHigh;
        public uint FileIndexLow;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern SafeFileHandle CreateFile(
        string name,
        uint access,
        uint share,
        IntPtr security,
        uint creation,
        uint flags,
        IntPtr template);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetFileInformationByHandle(
        SafeFileHandle file,
        out ByHandleFileInformation information);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetFileInformationByHandleEx(
        SafeFileHandle file,
        int informationClass,
        IntPtr information,
        uint informationLength);

    private static void AssertDefaultDataStreamOnly(SafeFileHandle handle)
    {
        int size = 4096;
        IntPtr buffer = IntPtr.Zero;
        try
        {
            while (true)
            {
                buffer = Marshal.AllocHGlobal(size);
                if (GetFileInformationByHandleEx(
                    handle,
                    FileStreamInfo,
                    buffer,
                    (uint)size))
                    break;
                int code = Marshal.GetLastWin32Error();
                Marshal.FreeHGlobal(buffer);
                buffer = IntPtr.Zero;
                if ((code != ErrorInsufficientBuffer && code != ErrorMoreData) ||
                    size >= MaximumStreamInfoBytes)
                    throw new Win32Exception(code);
                size = Math.Min(size * 2, MaximumStreamInfoBytes);
            }

            int offset = 0;
            bool foundDefault = false;
            while (true)
            {
                if (offset < 0 || offset > size - StreamHeaderBytes)
                    throw new InvalidDataException("invalid file stream inventory");
                IntPtr entry = IntPtr.Add(buffer, offset);
                uint next = unchecked((uint)Marshal.ReadInt32(entry, 0));
                uint nameBytes = unchecked((uint)Marshal.ReadInt32(entry, 4));
                if (nameBytes == 0 || (nameBytes & 1) != 0 ||
                    nameBytes > (uint)(size - offset - StreamHeaderBytes))
                    throw new InvalidDataException("invalid file stream name");
                string name = Marshal.PtrToStringUni(
                    IntPtr.Add(entry, StreamHeaderBytes),
                    checked((int)nameBytes / 2));
                if (!String.Equals(name, "::$DATA", StringComparison.Ordinal) ||
                    foundDefault)
                    throw new InvalidDataException(
                        "alternate data streams are forbidden");
                foundDefault = true;
                if (next == 0) break;
                if (next < StreamHeaderBytes || next > (uint)(size - offset))
                    throw new InvalidDataException("invalid file stream offset");
                offset = checked(offset + (int)next);
            }
            if (!foundDefault)
                throw new InvalidDataException("default data stream is absent");
        }
        finally
        {
            if (buffer != IntPtr.Zero) Marshal.FreeHGlobal(buffer);
        }
    }

    private static FileStream OpenNoFollow(string path)
    {
        SafeFileHandle handle = CreateFile(
            path,
            GenericRead,
            ShareRead | ShareWrite,
            IntPtr.Zero,
            OpenExisting,
            OpenReparsePoint,
            IntPtr.Zero);
        if (handle.IsInvalid)
        {
            int code = Marshal.GetLastWin32Error();
            handle.Dispose();
            throw new Win32Exception(code);
        }
        ByHandleFileInformation information;
        if (!GetFileInformationByHandle(handle, out information))
        {
            int code = Marshal.GetLastWin32Error();
            handle.Dispose();
            throw new Win32Exception(code);
        }
        if ((information.FileAttributes & AttributeReparsePoint) != 0 ||
            (information.FileAttributes & AttributeDirectory) != 0 ||
            information.NumberOfLinks != 1)
        {
            handle.Dispose();
            throw new InvalidDataException(
                "source must be a single-link regular non-reparse file");
        }
        try
        {
            AssertDefaultDataStreamOnly(handle);
        }
        catch
        {
            handle.Dispose();
            throw;
        }
        return new FileStream(handle, FileAccess.Read, 65536, false);
    }

    private static string Hex(byte[] bytes)
    {
        return BitConverter.ToString(bytes).Replace("-", "").ToLowerInvariant();
    }

    public static S3CRecoveryFileIdentity HashNoFollow(string path)
    {
        using (FileStream stream = OpenNoFollow(path))
        using (SHA256 sha = SHA256.Create())
        {
            return new S3CRecoveryFileIdentity {
                SizeBytes = stream.Length,
                Sha256 = Hex(sha.ComputeHash(stream))
            };
        }
    }

    public static S3CRecoveryFileIdentity InspectNoFollow(string path)
    {
        using (FileStream stream = OpenNoFollow(path))
        {
            return new S3CRecoveryFileIdentity {
                SizeBytes = stream.Length,
                Sha256 = ""
            };
        }
    }

    public static byte[] ReadNoFollow(string path, int maxBytes)
    {
        using (FileStream stream = OpenNoFollow(path))
        {
            if (stream.Length <= 0 || stream.Length > maxBytes)
                throw new InvalidDataException("file size is outside the bound");
            byte[] result = new byte[stream.Length];
            int offset = 0;
            while (offset < result.Length)
            {
                int count = stream.Read(result, offset, result.Length - offset);
                if (count <= 0) throw new EndOfStreamException();
                offset += count;
            }
            return result;
        }
    }

    public static S3CRecoveryFileIdentity CopyNoFollow(
        string source,
        string destination)
    {
        S3CRecoveryFileIdentity before = HashNoFollow(source);
        try
        {
            using (FileStream input = OpenNoFollow(source))
            using (FileStream output = new FileStream(
                destination,
                FileMode.CreateNew,
                FileAccess.Write,
                FileShare.None,
                65536,
                FileOptions.WriteThrough))
            {
                input.CopyTo(output, 65536);
                output.Flush(true);
            }
            S3CRecoveryFileIdentity after = HashNoFollow(destination);
            if (before.SizeBytes != after.SizeBytes ||
                !String.Equals(before.Sha256, after.Sha256, StringComparison.Ordinal))
                throw new InvalidDataException("source and destination differ");
            return after;
        }
        catch
        {
            try { if (File.Exists(destination)) File.Delete(destination); }
            catch { }
            throw;
        }
    }
}

public sealed class S3CRecoveryOwnerFence : IDisposable
{
    [StructLayout(LayoutKind.Sequential)]
    private struct Overlapped
    {
        public UIntPtr Internal;
        public UIntPtr InternalHigh;
        public uint Offset;
        public uint OffsetHigh;
        public IntPtr Event;
    }

    private const uint LockExclusive = 0x00000002;
    private const uint LockFailImmediately = 0x00000001;
    private FileStream file;
    private Overlapped overlapped;

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool LockFileEx(
        SafeFileHandle file,
        uint flags,
        uint reserved,
        uint low,
        uint high,
        ref Overlapped overlapped);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool UnlockFileEx(
        SafeFileHandle file,
        uint reserved,
        uint low,
        uint high,
        ref Overlapped overlapped);

    private S3CRecoveryOwnerFence(FileStream value)
    {
        file = value;
        overlapped = new Overlapped();
    }

    public static S3CRecoveryOwnerFence Acquire(string path)
    {
        if (!File.Exists(path))
            throw new FileNotFoundException("owner lock is absent", path);
        FileStream stream = new FileStream(
            path,
            FileMode.Open,
            FileAccess.ReadWrite,
            FileShare.ReadWrite);
        var result = new S3CRecoveryOwnerFence(stream);
        if (LockFileEx(
            stream.SafeFileHandle,
            LockExclusive | LockFailImmediately,
            0,
            1,
            0,
            ref result.overlapped))
            return result;
        int code = Marshal.GetLastWin32Error();
        stream.Dispose();
        if (code == 32 || code == 33)
            throw new InvalidOperationException("OWNER_ACTIVE");
        throw new Win32Exception(code);
    }

    public void Dispose()
    {
        if (file == null) return;
        UnlockFileEx(file.SafeFileHandle, 0, 1, 0, ref overlapped);
        file.Dispose();
        file = null;
    }
}

public static class S3CRecoveryProcessJob
{
    private const UInt32 ExtendedLimitInformation = 9;
    private const UInt32 KillOnJobClose = 0x00002000;

    [StructLayout(LayoutKind.Sequential)]
    private struct BasicLimitInformation
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
    private struct ExtendedLimitInformationValue
    {
        public BasicLimitInformation BasicLimitInformation;
        public IoCounters IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateJobObject(IntPtr attributes, string name);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetInformationJobObject(
        IntPtr job, UInt32 informationClass, IntPtr information,
        UInt32 informationLength);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool AssignProcessToJobObject(
        IntPtr job, IntPtr process);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool TerminateJobObject(IntPtr job, UInt32 exitCode);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool CloseHandle(IntPtr handle);

    public static IntPtr Create(Process process)
    {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero)
            throw new Win32Exception(Marshal.GetLastWin32Error());
        IntPtr information = IntPtr.Zero;
        bool assigned = false;
        try
        {
            var value = new ExtendedLimitInformationValue();
            value.BasicLimitInformation.LimitFlags = KillOnJobClose;
            int size = Marshal.SizeOf(value);
            information = Marshal.AllocHGlobal(size);
            Marshal.StructureToPtr(value, information, false);
            if (!SetInformationJobObject(
                job, ExtendedLimitInformation, information, (UInt32)size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            if (!AssignProcessToJobObject(job, process.Handle))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            assigned = true;
            return job;
        }
        finally
        {
            if (information != IntPtr.Zero) Marshal.FreeHGlobal(information);
            if (!assigned) CloseHandle(job);
        }
    }

    public static void Terminate(IntPtr job)
    {
        if (job != IntPtr.Zero && !TerminateJobObject(job, 124))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    public static void Close(IntPtr job)
    {
        if (job != IntPtr.Zero && !CloseHandle(job))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }
}
'@ -ErrorAction Stop
    } catch {
        [Console]::Error.WriteLine(
            'S3C_RECOVERY_FAIL code=RECOVERY_RUNTIME_UNAVAILABLE'
        )
        exit 1
    }
}

$startedAt = [DateTimeOffset]::UtcNow
$status = 'HARNESS_ERROR'
$processExit = 1
$ownerFence = $null
$sourceSnapshot = @()
$sourceUnchanged = $true
$cloneLocks = @()
$copyVerification = @()
$sourceCellId = ''
$sourceStatus = ''
$sourceExitLock = $null
$sourceInputLocksLock = $null
$sourcePlanLock = $null
$sourceBinaryLock = $null
$backupResult = $null
$verifyResult = $null
$bundleVerified = $false
$bundleFacts = $null
$databasePath = ''
$artifactRoot = ''
$recoveryPath = ''
$rawCloneRoot = ''
$bundlePath = ''

try {
    if ($RecoveryId -cnotmatch '^[a-z0-9][a-z0-9._-]{0,95}$') {
        Stop-S3CRecovery -Code 'RECOVERY_ID_INVALID'
    }
    $sourceCell = Get-S3CRecoveryFullPath `
        -Value $SourceCellPath `
        -Code 'SOURCE_CELL_INVALID'
    $output = Get-S3CRecoveryFullPath `
        -Value $OutputRoot `
        -Code 'OUTPUT_ROOT_INVALID'
    Assert-S3CRecoveryDirectory `
        -Path $sourceCell `
        -Code 'SOURCE_CELL_INVALID'
    $recoveryPath = Join-Path $output $RecoveryId
    if ((Test-S3CRecoveryPathContains `
        -Parent $sourceCell `
        -Candidate $recoveryPath `
        -AllowEqual) -or
        (Test-S3CRecoveryPathContains `
            -Parent $recoveryPath `
            -Candidate $sourceCell `
            -AllowEqual)) {
        Stop-S3CRecovery -Code 'SOURCE_OUTPUT_PATH_OVERLAP'
    }

    $exitPath = Join-Path $sourceCell 'exit.json'
    $inputLocksPath = Join-Path $sourceCell 'input-locks.json'
    $planPath = Join-Path $sourceCell 'cell-plan.json'
    $exitRecord = Read-S3CRecoveryJsonObject `
        -Path $exitPath `
        -Code 'SOURCE_EXIT_INVALID'
    $inputLocks = Read-S3CRecoveryJsonObject `
        -Path $inputLocksPath `
        -Code 'SOURCE_INPUT_LOCKS_INVALID'
    $cellPlan = Read-S3CRecoveryJsonObject `
        -Path $planPath `
        -Code 'SOURCE_PLAN_INVALID'
    if ([string]$exitRecord.schema_version -cne
        'freeagent.s3c-real-cell-exit/v1' -or
        [string]::IsNullOrWhiteSpace([string]$exitRecord.cell_id) -or
        [string]::IsNullOrWhiteSpace([string]$exitRecord.status) -or
        -not [bool]$exitRecord.raw_work_retained) {
        Stop-S3CRecovery -Code 'SOURCE_EXIT_INVALID'
    }
    $sourceCellId = [string]$exitRecord.cell_id
    $sourceStatus = [string]$exitRecord.status
    if ($sourceStatus -ceq 'COMPLETE') {
        Stop-S3CRecovery -Code 'SOURCE_CELL_COMPLETE'
    }
    if ([string](Get-Item -LiteralPath $sourceCell -Force).Name -cne
        $sourceCellId) {
        Stop-S3CRecovery -Code 'SOURCE_CELL_ID_MISMATCH'
    }
    if ([string]$inputLocks.schema_version -cne
        'freeagent.s3c-input-locks/v1' -or
        -not ($inputLocks.binary -is [pscustomobject])) {
        Stop-S3CRecovery -Code 'SOURCE_INPUT_LOCKS_INVALID'
    }
    if ([string]$cellPlan.schema_version -cne
        'freeagent.s3c-cell-plan/v1' -or
        [string]$cellPlan.cell_id -cne $sourceCellId -or
        -not ($cellPlan.paths -is [pscustomobject]) -or
        [string]$cellPlan.paths.database -cne 'work/current.sqlite' -or
        [string]$cellPlan.paths.artifact_root -cne 'work/artifacts' -or
        [string]$cellPlan.paths.backup_bundle -cne 'archive/store.bundle' -or
        [string]$cellPlan.secret_environment_name -cnotmatch
            '^[A-Za-z_][A-Za-z0-9_]{0,127}$') {
        Stop-S3CRecovery -Code 'SOURCE_PLAN_INVALID'
    }
    $secretEnvironmentName = [string]$cellPlan.secret_environment_name
    $binaryRelative = [string]$inputLocks.binary.relative_path
    $frozenBinary = Resolve-S3CRecoveryCellRelativePath `
        -CellRoot $sourceCell `
        -RelativePath $binaryRelative `
        -Code 'SOURCE_BINARY_INVALID'
    $databasePath = Resolve-S3CRecoveryCellRelativePath `
        -CellRoot $sourceCell `
        -RelativePath 'work/current.sqlite' `
        -Code 'SOURCE_DATABASE_INVALID'
    $artifactRoot = Resolve-S3CRecoveryCellRelativePath `
        -CellRoot $sourceCell `
        -RelativePath 'work/artifacts' `
        -Code 'SOURCE_ARTIFACTS_INVALID'
    $ownerLockPath = $databasePath + '.freeagent.owner.lock'
    Assert-S3CRecoveryDirectory `
        -Path $artifactRoot `
        -Code 'SOURCE_ARTIFACTS_INVALID'
    foreach ($fileCheck in @(
        @{ Path = $exitPath; Code = 'SOURCE_EXIT_INVALID' },
        @{ Path = $inputLocksPath; Code = 'SOURCE_INPUT_LOCKS_INVALID' },
        @{ Path = $planPath; Code = 'SOURCE_PLAN_INVALID' },
        @{ Path = $frozenBinary; Code = 'SOURCE_BINARY_INVALID' },
        @{ Path = $databasePath; Code = 'SOURCE_DATABASE_INVALID' }
    )) {
        [void](Get-S3CRecoveryFileLock `
            -Path $fileCheck.Path `
            -Code $fileCheck.Code)
    }
    Assert-S3CRecoveryNoReparseAncestry `
        -Path $ownerLockPath `
        -Code 'SOURCE_OWNER_LOCK_INVALID'
    try {
        $ownerLockIdentity = [S3CRecoveryFile]::InspectNoFollow($ownerLockPath)
    } catch {
        Stop-S3CRecovery -Code 'SOURCE_OWNER_LOCK_INVALID'
    }
    if ($ownerLockIdentity.SizeBytes -ne 0) {
        Stop-S3CRecovery -Code 'SOURCE_OWNER_LOCK_INVALID'
    }
    if (-not (Test-S3CRecoverySidecarsAbsent -DatabasePath $databasePath)) {
        Stop-S3CRecovery -Code 'SOURCE_SQLITE_SIDECAR_PRESENT'
    }
    try {
        $ownerFence = [S3CRecoveryOwnerFence]::Acquire($ownerLockPath)
    } catch {
        Stop-S3CRecovery -Code 'SOURCE_OWNER_ACTIVE'
    }
    if (-not (Test-S3CRecoverySidecarsAbsent -DatabasePath $databasePath)) {
        Stop-S3CRecovery -Code 'SOURCE_SQLITE_SIDECAR_PRESENT'
    }

    $sourceExitLock = Get-S3CRecoveryFileLock `
        -Path $exitPath `
        -Code 'SOURCE_EXIT_INVALID'
    $sourceInputLocksLock = Get-S3CRecoveryFileLock `
        -Path $inputLocksPath `
        -Code 'SOURCE_INPUT_LOCKS_INVALID'
    $sourcePlanLock = Get-S3CRecoveryFileLock `
        -Path $planPath `
        -Code 'SOURCE_PLAN_INVALID'
    $sourceBinaryLock = Get-S3CRecoveryFileLock `
        -Path $frozenBinary `
        -Code 'SOURCE_BINARY_INVALID'
    if ($sourceBinaryLock.SizeBytes -ne [int64]$inputLocks.binary.size_bytes -or
        $sourceBinaryLock.Sha256 -cne [string]$inputLocks.binary.sha256) {
        Stop-S3CRecovery -Code 'SOURCE_BINARY_LOCK_MISMATCH'
    }
    $sourceSnapshotList = New-Object 'Collections.Generic.List[object]'
    foreach ($fixed in @(
        @{ Relative = 'exit.json'; Path = $exitPath; Kind = 'metadata' },
        @{ Relative = 'input-locks.json'; Path = $inputLocksPath; Kind = 'metadata' },
        @{ Relative = 'cell-plan.json'; Path = $planPath; Kind = 'metadata' },
        @{ Relative = $binaryRelative.Replace('\', '/'); Path = $frozenBinary; Kind = 'binary' },
        @{ Relative = 'work/current.sqlite'; Path = $databasePath; Kind = 'database' }
    )) {
        $lock = Get-S3CRecoveryFileLock `
            -Path $fixed.Path `
            -Code 'SOURCE_INVENTORY_INVALID'
        $sourceSnapshotList.Add([pscustomobject]@{
            RelativePath = [string]$fixed.Relative
            FullPath = [string]$fixed.Path
            Kind = [string]$fixed.Kind
            SizeBytes = $lock.SizeBytes
            Sha256 = $lock.Sha256
        })
    }
    foreach ($artifact in @(
        Get-S3CRecoverySafeTreeFiles `
            -Root $artifactRoot `
            -Code 'SOURCE_ARTIFACTS_INVALID'
    )) {
        $relative = ConvertTo-S3CRecoveryRelativePath `
            -Root $artifactRoot `
            -Path $artifact.FullName `
            -Code 'SOURCE_ARTIFACTS_INVALID'
        $lock = Get-S3CRecoveryFileLock `
            -Path $artifact.FullName `
            -Code 'SOURCE_ARTIFACTS_INVALID'
        $sourceSnapshotList.Add([pscustomobject]@{
            RelativePath = $relative
            FullPath = $artifact.FullName
            Kind = 'artifact'
            SizeBytes = $lock.SizeBytes
            Sha256 = $lock.Sha256
        })
    }
    $sourceSnapshot = $sourceSnapshotList.ToArray()

    if (Test-Path -LiteralPath $output) {
        Assert-S3CRecoveryDirectory `
            -Path $output `
            -Code 'OUTPUT_ROOT_INVALID'
    } else {
        Assert-S3CRecoveryNoReparseAncestry `
            -Path $output `
            -Code 'OUTPUT_ROOT_UNSAFE'
        try { [void][IO.Directory]::CreateDirectory($output) } catch {
            Stop-S3CRecovery -Code 'OUTPUT_ROOT_INVALID'
        }
        Assert-S3CRecoveryDirectory `
            -Path $output `
            -Code 'OUTPUT_ROOT_UNSAFE'
    }
    $script:RecoveryPath = $recoveryPath
    $createCode = [S3CRecoveryExclusiveDirectory]::Create($recoveryPath)
    if ($createCode -eq 80 -or $createCode -eq 183) {
        Stop-S3CRecovery -Code 'RECOVERY_COLLISION'
    }
    if ($createCode -ne 0) {
        Stop-S3CRecovery -Code 'RECOVERY_CREATE_FAILED'
    }
    $script:RecoveryCreated = $true

    $rawCloneRoot = Join-Path $recoveryPath 'raw-clone'
    $cloneArtifactRoot = Join-Path $rawCloneRoot 'artifacts'
    $cloneMetadataRoot = Join-Path $rawCloneRoot 'source-metadata'
    $archiveRoot = Join-Path $recoveryPath 'archive'
    foreach ($directory in @(
        $rawCloneRoot, $cloneArtifactRoot, $cloneMetadataRoot, $archiveRoot
    )) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    $cloneDatabase = Join-Path $rawCloneRoot 'current.sqlite'
    $cloneOwnerLock = $cloneDatabase + '.freeagent.owner.lock'
    $bundlePath = Join-Path $archiveRoot 'store.bundle'
    $copyList = New-Object 'Collections.Generic.List[object]'
    foreach ($copy in @(
        @{ Source = $databasePath; Destination = $cloneDatabase; SourceRelative = 'work/current.sqlite'; CloneRelative = 'current.sqlite' },
        @{ Source = $exitPath; Destination = (Join-Path $cloneMetadataRoot 'exit.json'); SourceRelative = 'exit.json'; CloneRelative = 'source-metadata/exit.json' },
        @{ Source = $inputLocksPath; Destination = (Join-Path $cloneMetadataRoot 'input-locks.json'); SourceRelative = 'input-locks.json'; CloneRelative = 'source-metadata/input-locks.json' },
        @{ Source = $planPath; Destination = (Join-Path $cloneMetadataRoot 'cell-plan.json'); SourceRelative = 'cell-plan.json'; CloneRelative = 'source-metadata/cell-plan.json' }
    )) {
        $lock = Copy-S3CRecoveryNoFollow `
            -Source $copy.Source `
            -Destination $copy.Destination `
            -Code 'RAW_CLONE_COPY_FAILED'
        $copyList.Add([ordered]@{
            source_relative_path = $copy.SourceRelative
            clone_relative_path = $copy.CloneRelative
            size_bytes = $lock.SizeBytes
            sha256 = $lock.Sha256
        })
    }
    $cloneOwnerStream = $null
    try {
        $cloneOwnerStream = New-Object IO.FileStream(
            $cloneOwnerLock,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $cloneOwnerStream.Flush($true)
    } catch {
        Stop-S3CRecovery -Code 'RAW_CLONE_OWNER_LOCK_FAILED'
    } finally {
        if ($null -ne $cloneOwnerStream) { $cloneOwnerStream.Dispose() }
    }
    foreach ($artifact in @($sourceSnapshot | Where-Object {
        $_.Kind -ceq 'artifact'
    })) {
        $destination = Join-Path `
            $cloneArtifactRoot `
            $artifact.RelativePath.Replace('/', '\')
        $lock = Copy-S3CRecoveryNoFollow `
            -Source $artifact.FullPath `
            -Destination $destination `
            -Code 'RAW_CLONE_COPY_FAILED'
        $copyList.Add([ordered]@{
            source_relative_path = 'work/artifacts/' + $artifact.RelativePath
            clone_relative_path = 'artifacts/' + $artifact.RelativePath
            size_bytes = $lock.SizeBytes
            sha256 = $lock.Sha256
        })
    }
    $copyVerification = @($copyList | Sort-Object clone_relative_path)
    $cloneLocksBefore = Get-S3CRecoveryTreeLocks `
        -Root $rawCloneRoot `
        -Code 'RAW_CLONE_VERIFY_FAILED'

    $backupResult = Invoke-S3CRecoveryPhase `
        -Phase 'backup' `
        -Executable $frozenBinary `
        -Arguments @(
            'backup', '--db', $cloneDatabase,
            '--artifact-root', $cloneArtifactRoot,
            '--out', $bundlePath
        ) `
        -WorkingDirectory $recoveryPath `
        -ExpectedBundle $bundlePath `
        -SecretEnvironmentName $secretEnvironmentName
    $backupAccepted = $backupResult.ExitCode -eq 0 -and
        $backupResult.JsonValid -and
        -not $backupResult.CaptureFailed -and
        -not $backupResult.LeakDetected -and
        -not $backupResult.CleanupFailed

    $bundleCanVerify = Test-Path -LiteralPath $bundlePath -PathType Container
    if ($bundleCanVerify -and -not $backupResult.LeakDetected -and
        -not $backupResult.CleanupFailed) {
        Assert-S3CRecoveryDirectory `
            -Path $bundlePath `
            -Code 'BACKUP_BUNDLE_UNSAFE'
        $verifyResult = Invoke-S3CRecoveryPhase `
            -Phase 'backup-verify' `
            -Executable $frozenBinary `
            -Arguments @('backup-verify', '--bundle', $bundlePath) `
            -WorkingDirectory $recoveryPath `
            -ExpectedBundle $bundlePath `
            -SecretEnvironmentName $secretEnvironmentName
    }
    $verifyAccepted = $null -ne $verifyResult -and
        $verifyResult.ExitCode -eq 0 -and
        $verifyResult.JsonValid -and
        -not $verifyResult.CaptureFailed -and
        -not $verifyResult.LeakDetected -and
        -not $verifyResult.CleanupFailed

    if (-not $backupAccepted) {
        $status = if ($backupResult.LeakDetected -or
            $backupResult.CleanupFailed) {
            'CAPTURE_BLOCKED'
        } else {
            'BACKUP_FAILED'
        }
        Stop-S3CRecovery -Code $status
    }
    if (-not $verifyAccepted) {
        $status = if ($null -ne $verifyResult -and
            ($verifyResult.LeakDetected -or $verifyResult.CleanupFailed)) {
            'CAPTURE_BLOCKED'
        } else {
            'BACKUP_VERIFY_FAILED'
        }
        Stop-S3CRecovery -Code $status
    }
    if ($backupResult.Payload.ManifestIdentity -cne
        $verifyResult.Payload.ManifestIdentity -or
        $backupResult.Payload.ManifestDigest -cne
        $verifyResult.Payload.ManifestDigest -or
        $backupResult.Payload.StoreInstanceId -cne
        $verifyResult.Payload.StoreInstanceId) {
        $status = 'BACKUP_VERIFY_FAILED'
        Stop-S3CRecovery -Code 'BACKUP_VERIFY_MISMATCH'
    }
    $manifestPath = Join-Path $bundlePath 'manifest.json'
    $manifestLock = Get-S3CRecoveryFileLock `
        -Path $manifestPath `
        -Code 'BACKUP_MANIFEST_INVALID'
    $bundleFacts = [ordered]@{
        verified = $true
        relative_path = 'archive/store.bundle'
        format_version = [string]$backupResult.Payload.Manifest.format_version
        manifest_digest = $backupResult.Payload.ManifestDigest
        manifest_file_sha256 = $manifestLock.Sha256
        store_instance_id = $backupResult.Payload.StoreInstanceId
        database_sha256 = $backupResult.Payload.DatabaseSha256
    }
    $bundleVerified = $true

    if (-not (Test-S3CRecoverySidecarsAbsent -DatabasePath $cloneDatabase)) {
        $status = 'RAW_CLONE_CHANGED'
        Stop-S3CRecovery -Code 'RAW_CLONE_SQLITE_SIDECAR_PRESENT'
    }
    $cloneLocks = Get-S3CRecoveryTreeLocks `
        -Root $rawCloneRoot `
        -Code 'RAW_CLONE_VERIFY_FAILED'
    if (($cloneLocksBefore | ConvertTo-Json -Depth 10 -Compress) -cne
        ($cloneLocks | ConvertTo-Json -Depth 10 -Compress)) {
        $status = 'RAW_CLONE_CHANGED'
        Stop-S3CRecovery -Code 'RAW_CLONE_CHANGED'
    }
    $sourceUnchanged = Test-S3CRecoverySourceUnchanged `
        -Snapshot $sourceSnapshot `
        -ArtifactRoot $artifactRoot `
        -DatabasePath $databasePath
    if (-not $sourceUnchanged) {
        $status = 'SOURCE_CHANGED'
        Stop-S3CRecovery -Code 'SOURCE_CHANGED'
    }
    $status = 'COMPLETE'
    $script:FailureCode = ''
    $processExit = 0
} catch {
    if ([string]::IsNullOrWhiteSpace($script:FailureExceptionType)) {
        $script:FailureExceptionType = $_.Exception.GetType().FullName
    }
    if ($script:FailureCode -ceq 'UNCLASSIFIED') {
        $script:FailureCode = 'HARNESS_UNEXPECTED'
    }
    if ($status -ceq 'HARNESS_ERROR') {
        $status = 'HARNESS_ERROR'
    }
    $processExit = if ($script:RecoveryCreated) { 2 } else { 1 }
} finally {
    if ($null -ne $ownerFence -and $sourceSnapshot.Count -gt 0) {
        $finalSourceCheck = Test-S3CRecoverySourceUnchanged `
            -Snapshot $sourceSnapshot `
            -ArtifactRoot $artifactRoot `
            -DatabasePath $databasePath
        if (-not $finalSourceCheck) {
            $sourceUnchanged = $false
            $status = 'SOURCE_CHANGED'
            $script:FailureCode = 'SOURCE_CHANGED'
            $processExit = 2
        }
    }
    if ($null -ne $ownerFence) {
        try { $ownerFence.Dispose() } catch {
            $status = 'OWNER_FENCE_RELEASE_FAILED'
            $script:FailureCode = 'OWNER_FENCE_RELEASE_FAILED'
            $processExit = 2
        }
    }
}

if ($script:RecoveryCreated) {
    if ($cloneLocks.Count -eq 0 -and
        -not [string]::IsNullOrWhiteSpace($rawCloneRoot) -and
        (Test-Path -LiteralPath $rawCloneRoot -PathType Container)) {
        try {
            $cloneLocks = Get-S3CRecoveryTreeLocks `
                -Root $rawCloneRoot `
                -Code 'RAW_CLONE_VERIFY_FAILED'
        } catch {}
    }
    $phaseFacts = [ordered]@{
        backup_exit_code = if ($null -eq $backupResult) {
            $null
        } else {
            $backupResult.ExitCode
        }
        backup_timed_out = [bool]($null -ne $backupResult -and
            $backupResult.TimedOut)
        backup_capture_failed = [bool]($null -ne $backupResult -and
            $backupResult.CaptureFailed)
        backup_verify_exit_code = if ($null -eq $verifyResult) {
            $null
        } else {
            $verifyResult.ExitCode
        }
        backup_verify_timed_out = [bool]($null -ne $verifyResult -and
            $verifyResult.TimedOut)
        backup_verify_capture_failed = [bool]($null -ne $verifyResult -and
            $verifyResult.CaptureFailed)
    }
    $record = [ordered]@{
        schema_version = 'freeagent.s3c-cell-recovery/v1'
        recovery_id = $RecoveryId
        started_at_utc = $startedAt.ToString('O')
        finished_at_utc = [DateTimeOffset]::UtcNow.ToString('O')
        status = $status
        failure_code = $script:FailureCode
        failure_exception_type = $script:FailureExceptionType
        source_cell = [ordered]@{
            cell_id = $sourceCellId
            terminal_status = $sourceStatus
            exit_sha256 = if ($null -eq $sourceExitLock) { '' } else {
                $sourceExitLock.Sha256
            }
            input_locks_sha256 = if ($null -eq $sourceInputLocksLock) { '' } else {
                $sourceInputLocksLock.Sha256
            }
            cell_plan_sha256 = if ($null -eq $sourcePlanLock) { '' } else {
                $sourcePlanLock.Sha256
            }
            frozen_binary = if ($null -eq $sourceBinaryLock) { $null } else {
                [ordered]@{
                    relative_path = $binaryRelative.Replace('\', '/')
                    size_bytes = $sourceBinaryLock.SizeBytes
                    sha256 = $sourceBinaryLock.Sha256
                }
            }
            unchanged = [bool]$sourceUnchanged
        }
        copy_verification = $copyVerification
        clone_locks = $cloneLocks
        bundle_verification = if ($null -eq $bundleFacts) {
            [ordered]@{ verified = $false }
        } else {
            $bundleFacts
        }
        phases = $phaseFacts
        semantic_replay = $false
        report_recovered = $false
        automatic_retry = $false
        manual_review_required = ($status -cne 'COMPLETE')
    }
    try {
        Write-S3CRecoveryAtomicRecord `
            -RecoveryPath $recoveryPath `
            -Value $record
    } catch {
        [Console]::Error.WriteLine(
            'S3C_RECOVERY_FAIL code=RECOVERY_RECORD_COMMIT_FAILED'
        )
        exit 1
    }
    [Console]::Out.WriteLine(
        'S3C_RECOVERY status=' + $status + ' recovery_id=' + $RecoveryId
    )
    if ($status -cne 'COMPLETE') {
        [Console]::Error.WriteLine(
            'S3C_RECOVERY_REVIEW_REQUIRED code=' + $script:FailureCode
        )
    }
} else {
    [Console]::Error.WriteLine(
        'S3C_RECOVERY_FAIL code=' + $script:FailureCode
    )
}

exit $processExit
