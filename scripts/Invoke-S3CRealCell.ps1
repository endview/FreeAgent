[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$FreeAgentPath,
    [Parameter(Mandatory = $true)][string]$SeedPath,
    [Parameter(Mandatory = $true)][string]$ScenarioPath,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [Parameter(Mandatory = $true)][string]$CellId,
    [Parameter(Mandatory = $true)]
    [ValidateSet('Off', 'On')]
    [string]$SchedulerMode,
    [ValidateRange(0, 4294967295)]
    [uint64]$SchedulerGlobalWorkers = 0,
    [ValidateRange(0, 4294967295)]
    [uint64]$SchedulerWorkspaceWorkers = 0,
    [ValidateRange(0, 4294967295)]
    [uint64]$SchedulerFamilyWorkers = 0,
    [ValidateRange(1, 3)]
    [int]$Repetitions = 1,
    [Parameter(Mandatory = $true)][string]$SecretEnvironmentName
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object Text.UTF8Encoding($false, $true)
$script:PathComparison = [StringComparison]::OrdinalIgnoreCase
$script:FailureCode = 'UNCLASSIFIED'
$script:FailureExceptionType = ''
$script:CellCreated = $false
$script:CellPath = $null
$script:ExitWritten = $false
$script:OperationalTimeoutMilliseconds = 1800000
$script:EvaluationTimeoutMilliseconds = 21600000
$script:OutputCapacitySafetyMarginBytes = [uint64]268435456

function Fail-S3CRealCell {
    param([Parameter(Mandatory = $true)][string]$Code)
    $script:FailureCode = $Code
    throw 'S3C_REAL_CELL_STOP'
}

function Get-S3CFullPath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-S3CRealCell -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-S3CRealCell -Code $Code
        }
    }
    try {
        return [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-S3CRealCell -Code $Code
    }
}

function Assert-S3CNoReparseAncestry {
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
                Fail-S3CRealCell -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-S3CRealCell -Code $Code
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

function Test-S3CPathContains {
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

function Assert-S3CPathsDoNotOverlapCell {
    param(
        [Parameter(Mandatory = $true)][string]$OutputRootPath,
        [Parameter(Mandatory = $true)][string]$FutureCellPath,
        [Parameter(Mandatory = $true)][object[]]$SourcePaths
    )
    foreach ($sourcePath in $SourcePaths) {
        $source = [string]$sourcePath
        $item = Get-Item -LiteralPath $source -Force
        if (Test-S3CPathContains `
            -Parent $FutureCellPath `
            -Candidate $source `
            -AllowEqual) {
            Fail-S3CRealCell -Code 'INPUT_OUTPUT_PATH_OVERLAP'
        }
        if ($item -is [IO.DirectoryInfo]) {
            if ((Test-S3CPathContains `
                -Parent $source `
                -Candidate $OutputRootPath `
                -AllowEqual) -or
                (Test-S3CPathContains `
                    -Parent $source `
                    -Candidate $FutureCellPath `
                    -AllowEqual)) {
                Fail-S3CRealCell -Code 'INPUT_OUTPUT_PATH_OVERLAP'
            }
        } elseif ([string]::Equals(
            $source,
            $OutputRootPath,
            $script:PathComparison
        ) -or [string]::Equals(
            $source,
            $FutureCellPath,
            $script:PathComparison
        )) {
            Fail-S3CRealCell -Code 'INPUT_OUTPUT_PATH_OVERLAP'
        }
    }
}

function Assert-S3CRegularFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail-S3CRealCell -Code $Code
    }
    Assert-S3CNoReparseAncestry -Path $Path -Code $Code
    try {
        $item = Get-Item -LiteralPath $Path -Force
    } catch {
        Fail-S3CRealCell -Code $Code
    }
    if (-not ($item -is [IO.FileInfo]) -or $item.Length -le 0) {
        Fail-S3CRealCell -Code $Code
    }
}

function Add-S3CCheckedFileLength {
    param(
        [Parameter(Mandatory = $true)][uint64]$Current,
        [Parameter(Mandatory = $true)][IO.FileInfo]$File
    )
    [uint64]$length = $File.Length
    if ($length -gt ([uint64]::MaxValue - $Current)) {
        Fail-S3CRealCell -Code 'INPUT_SIZE_OVERFLOW'
    }
    return [uint64]($Current + $length)
}

function Get-S3COutputAvailableBytes {
    param([Parameter(Mandatory = $true)][string]$OutputRootPath)
    try {
        $volumeRoot = [IO.Path]::GetPathRoot($OutputRootPath)
        if ([string]::IsNullOrWhiteSpace($volumeRoot)) {
            throw 'output volume root is unavailable'
        }
        $drive = New-Object IO.DriveInfo($volumeRoot)
        if (-not $drive.IsReady -or $drive.AvailableFreeSpace -lt 0) {
            throw 'output volume is not ready'
        }
        return [uint64]$drive.AvailableFreeSpace
    } catch {
        Fail-S3CRealCell -Code 'OUTPUT_SPACE_QUERY_FAILED'
    }
}

function Assert-S3COutputCapacity {
    param(
        [Parameter(Mandatory = $true)][string]$OutputRootPath,
        [Parameter(Mandatory = $true)][uint64]$VerifiedSourceBytes
    )
    if ($VerifiedSourceBytes -gt (
        [uint64]::MaxValue - $script:OutputCapacitySafetyMarginBytes
    )) {
        Fail-S3CRealCell -Code 'INPUT_SIZE_OVERFLOW'
    }
    [uint64]$requiredBytes = $VerifiedSourceBytes +
        $script:OutputCapacitySafetyMarginBytes
    [uint64]$availableBytes = Get-S3COutputAvailableBytes `
        -OutputRootPath $OutputRootPath
    if ($availableBytes -lt $requiredBytes) {
        Fail-S3CRealCell -Code 'OUTPUT_SPACE_INSUFFICIENT'
    }
}

function Read-S3CJsonObject {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
        $text = $script:Utf8NoBom.GetString($bytes)
        $value = $text | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Fail-S3CRealCell -Code $Code
    }
    if ($null -eq $value -or -not ($value -is [pscustomobject])) {
        Fail-S3CRealCell -Code $Code
    }
    return $value
}

function Find-S3CArtifactRelativePath {
    param([AllowNull()]$Node)
    if ($null -eq $Node -or $Node -is [string] -or
        $Node -is [ValueType]) {
        return
    }
    if ($Node -is [Collections.IDictionary]) {
        foreach ($key in $Node.Keys) {
            if ([string]$key -ceq 'artifact_relative_path') {
                Write-Output ([string]$Node[$key])
            } else {
                Find-S3CArtifactRelativePath -Node $Node[$key]
            }
        }
        return
    }
    if ($Node -is [Collections.IEnumerable] -and
        -not ($Node -is [pscustomobject])) {
        foreach ($item in $Node) {
            Find-S3CArtifactRelativePath -Node $item
        }
        return
    }
    if ($Node -is [pscustomobject]) {
        foreach ($property in $Node.PSObject.Properties) {
            if ($property.Name -ceq 'artifact_relative_path') {
                Write-Output ([string]$property.Value)
            } else {
                Find-S3CArtifactRelativePath -Node $property.Value
            }
        }
    }
}

function Resolve-S3CArtifactPath {
    param(
        [Parameter(Mandatory = $true)][string]$SeedDirectory,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath.IndexOf(':') -ge 0) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    $segments = $RelativePath.Replace('/', '\').Split([char]92)
    if ($segments.Count -eq 0) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    foreach ($segment in $segments) {
        if ([string]::IsNullOrWhiteSpace($segment) -or
            $segment -ceq '.' -or $segment -ceq '..') {
            Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
        }
    }
    try {
        $resolved = [IO.Path]::GetFullPath((Join-Path $SeedDirectory $RelativePath))
    } catch {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    $prefix = $SeedDirectory.TrimEnd([char]92, [char]47) +
        [IO.Path]::DirectorySeparatorChar
    if (-not $resolved.StartsWith($prefix, $script:PathComparison)) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    if (-not (Test-Path -LiteralPath $resolved)) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_MISSING'
    }
    Assert-S3CNoReparseAncestry `
        -Path $resolved `
        -Code 'ARTIFACT_REFERENCE_UNSAFE'
    return [pscustomobject]@{
        RelativePath = ($segments -join [IO.Path]::DirectorySeparatorChar)
        SourcePath = $resolved
    }
}

function Get-S3CSafeTreeFiles {
    param([Parameter(Mandatory = $true)][string]$Root)
    $rootItem = Get-Item -LiteralPath $Root -Force
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_UNSAFE'
    }
    if ($rootItem -is [IO.FileInfo]) {
        Write-Output $rootItem
        return
    }
    if (-not ($rootItem -is [IO.DirectoryInfo])) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    $stack = New-Object 'Collections.Generic.Stack[string]'
    $stack.Push($rootItem.FullName)
    while ($stack.Count -gt 0) {
        $directory = $stack.Pop()
        try {
            $children = (New-Object IO.DirectoryInfo($directory)).
                EnumerateFileSystemInfos()
        } catch {
            Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_UNREADABLE'
        }
        foreach ($child in $children) {
            if (($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_UNSAFE'
            }
            if ($child -is [IO.DirectoryInfo]) {
                $stack.Push($child.FullName)
            } elseif ($child -is [IO.FileInfo]) {
                Write-Output $child
            } else {
                Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
            }
        }
    }
}

function Get-S3CRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Child
    )
    $prefix = $Parent.TrimEnd([char]92, [char]47) +
        [IO.Path]::DirectorySeparatorChar
    if (-not $Child.StartsWith($prefix, $script:PathComparison)) {
        Fail-S3CRealCell -Code 'ARTIFACT_REFERENCE_INVALID'
    }
    return $Child.Substring($prefix.Length)
}

function ConvertTo-S3CHex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes)).Replace('-', '').ToLowerInvariant()
}

function Copy-S3CSnapshotFile {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$Secret
    )
    $parent = [IO.Path]::GetDirectoryName($Destination)
    [void][IO.Directory]::CreateDirectory($parent)
    $input = $null
    $output = $null
    $sha = $null
    $copyFailed = $false
    try {
        $input = New-Object IO.FileStream(
            $Source,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        [byte[]]$digest = $sha.ComputeHash($input)
        $input.Position = 0
        $output = New-Object IO.FileStream(
            $Destination,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $input.CopyTo($output)
        $output.Flush($true)
        $length = $input.Length
    } catch {
        $copyFailed = $true
    } finally {
        if ($null -ne $output) { $output.Dispose() }
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
    }
    if ($copyFailed) {
        [void](Remove-S3CPendingChecked -Path $Destination)
        Fail-S3CRealCell -Code 'INPUT_SNAPSHOT_FAILED'
    }
    try {
        [byte[]]$snapshotBytes = [IO.File]::ReadAllBytes($Destination)
        if (Test-S3CExactSecret -Bytes $snapshotBytes -Secret $Secret) {
            [IO.File]::Delete($Destination)
            Fail-S3CRealCell -Code 'INPUT_SECRET_DETECTED'
        }
    } catch {
        if ($script:FailureCode -ceq 'INPUT_SECRET_DETECTED') { throw }
        Fail-S3CRealCell -Code 'INPUT_SNAPSHOT_FAILED'
    }
    try {
        $attributes = [IO.File]::GetAttributes($Destination)
        [IO.File]::SetAttributes(
            $Destination,
            ($attributes -bor [IO.FileAttributes]::ReadOnly)
        )
    } catch {
        Fail-S3CRealCell -Code 'INPUT_SNAPSHOT_FAILED'
    }
    return [pscustomobject]@{
        SizeBytes = [int64]$length
        Sha256 = ConvertTo-S3CHex -Bytes $digest
    }
}

function Write-S3CCreateNewBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes,
        [switch]$ReadOnly
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
        Fail-S3CRealCell -Code 'EVIDENCE_WRITE_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
    if ($ReadOnly) {
        try {
            [IO.File]::SetAttributes(
                $Path,
                ([IO.File]::GetAttributes($Path) -bor
                    [IO.FileAttributes]::ReadOnly)
            )
        } catch {
            Fail-S3CRealCell -Code 'EVIDENCE_WRITE_FAILED'
        }
    }
}

function Write-S3CCreateNewJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value,
        [switch]$ReadOnly
    )
    try {
        $json = $Value | ConvertTo-Json -Depth 30 -Compress
        [byte[]]$bytes = $script:Utf8NoBom.GetBytes($json + "`n")
    } catch {
        Fail-S3CRealCell -Code 'EVIDENCE_SERIALIZATION_FAILED'
    }
    Write-S3CCreateNewBytes -Path $Path -Bytes $bytes -ReadOnly:$ReadOnly
}

function Write-S3CAtomicExitJson {
    param(
        [Parameter(Mandatory = $true)][string]$CellPath,
        [Parameter(Mandatory = $true)]$Value
    )
    $destination = Join-Path $CellPath 'exit.json'
    $pending = Join-Path $CellPath (
        '.exit.pending.' + [Guid]::NewGuid().ToString('N')
    )
    $stream = $null
    $writeFailed = $false
    try {
        $json = $Value | ConvertTo-Json -Depth 30 -Compress
        [byte[]]$bytes = $script:Utf8NoBom.GetBytes($json + "`n")
        $stream = New-Object IO.FileStream(
            $pending,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
    } catch {
        $writeFailed = $true
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
    if ($writeFailed) {
        [void](Remove-S3CPendingChecked -Path $pending)
        Fail-S3CRealCell -Code 'EXIT_RECORD_WRITE_FAILED'
    }
    try {
        [IO.File]::Move($pending, $destination)
        [IO.File]::SetAttributes(
            $destination,
            ([IO.File]::GetAttributes($destination) -bor
                [IO.FileAttributes]::ReadOnly)
        )
    } catch {
        [void](Remove-S3CPendingChecked -Path $pending)
        Fail-S3CRealCell -Code 'EXIT_RECORD_COMMIT_FAILED'
    }
}

function Test-S3CBytePattern {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Haystack,
        [Parameter(Mandatory = $true)][byte[]]$Needle,
        [switch]$AsciiIgnoreCase
    )
    return [S3CByteScanner]::Contains(
        $Haystack,
        $Needle,
        [bool]$AsciiIgnoreCase
    )
}

function Test-S3CCaptureLeak {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$Secret
    )
    if (Test-S3CExactSecret -Bytes $Bytes -Secret $Secret) {
        return $true
    }
    $texts = New-Object 'Collections.Generic.List[string]'
    foreach ($encoding in @(
        $script:Utf8NoBom,
        (New-Object Text.UnicodeEncoding($false, $false, $true)),
        (New-Object Text.UnicodeEncoding($true, $false, $true))
    )) {
        try {
            $texts.Add($encoding.GetString($Bytes))
        } catch {}
    }
    foreach ($text in $texts) {
        if (Test-S3CLikelyCredentialText -Text $text) { return $true }
    }
    return $false
}

function Test-S3CCredentialCandidate {
    param([Parameter(Mandatory = $true)][string]$Value)
    if ($Value.Length -lt 16 -or $Value.Length -gt 512) { return $false }
    $normalized = $Value.ToLowerInvariant().Trim([char[]]'.-_~')
    if ($normalized -match '^(?:redact|mask|omit|remov|placeholder|example|sample|dummy)' -or
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
    $tokenSignal = $Value -match '[0-9._~+/\-]' -or
        ($Value -cmatch '[A-Z]' -and $Value -cmatch '[a-z]') -or
        ($Value.Length -ge 32 -and $unique.Count -ge 16)
    return [bool]$tokenSignal
}

function Test-S3CLikelyCredentialText {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)
    $patterns = @(
        '(?i)(?:^|[^A-Za-z0-9])Bearer[ \t]+(?<credential>[A-Za-z0-9][A-Za-z0-9._~+/\-]{15,511})',
        '(?i)Authorization(?:\\?")?[ \t]*(?::|=)[ \t]*(?:\\?")?[ \t]*(?:(?:Bearer|Basic|Token)[ \t]+)?(?<credential>[A-Za-z0-9][A-Za-z0-9._~+/\-]{15,511})'
    )
    foreach ($pattern in $patterns) {
        foreach ($match in [Text.RegularExpressions.Regex]::Matches(
            $Text,
            $pattern,
            [Text.RegularExpressions.RegexOptions]::CultureInvariant
        )) {
            if (Test-S3CCredentialCandidate `
                -Value $match.Groups['credential'].Value) {
                return $true
            }
        }
    }
    return $false
}

function Test-S3CExactSecret {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$Secret
    )
    foreach ($pattern in @(
        [Text.Encoding]::UTF8.GetBytes($Secret),
        [Text.Encoding]::Unicode.GetBytes($Secret),
        [Text.Encoding]::BigEndianUnicode.GetBytes($Secret)
    )) {
        if (Test-S3CBytePattern -Haystack $Bytes -Needle $pattern) {
            return $true
        }
    }
    return $false
}

function Assert-S3CSourceHasNoSecret {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Secret
    )
    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
    } catch {
        Fail-S3CRealCell -Code 'INPUT_SECRET_SCAN_FAILED'
    }
    if (Test-S3CExactSecret -Bytes $bytes -Secret $Secret) {
        Fail-S3CRealCell -Code 'INPUT_SECRET_DETECTED'
    }
}

function ConvertTo-S3CWindowsArgument {
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

function Join-S3CWindowsArguments {
    param([Parameter(Mandatory = $true)][string[]]$Values)
    return (($Values | ForEach-Object {
        ConvertTo-S3CWindowsArgument -Value ([string]$_)
    }) -join ' ')
}

function Test-S3CJsonCapture {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes
    )
    if ($Bytes.Length -eq 0) { return $false }
    try {
        $text = $script:Utf8NoBom.GetString($Bytes)
        $value = $text | ConvertFrom-Json -ErrorAction Stop
        return ($null -ne $value -and $value -is [pscustomobject])
    } catch {
        return $false
    }
}

function Test-S3CEvalReportCapture {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$ExitCode,
        [Parameter(Mandatory = $true)][int]$ExpectedRepetitions,
        [Parameter(Mandatory = $true)][string]$ExpectedExperimentId,
        [Parameter(Mandatory = $true)][string]$ExpectedSchedulerMode,
        [Parameter(Mandatory = $true)][uint64]$ExpectedGlobalWorkers,
        [Parameter(Mandatory = $true)][uint64]$ExpectedWorkspaceWorkers,
        [Parameter(Mandatory = $true)][uint64]$ExpectedFamilyWorkers
    )
    if (-not (Test-S3CJsonCapture -Bytes $Bytes)) { return $false }
    try {
        $report = $script:Utf8NoBom.GetString($Bytes) |
            ConvertFrom-Json -ErrorAction Stop
        $schemaProperty = $report.PSObject.Properties['schema_version']
        $errorProperty = $report.PSObject.Properties['first_error']
        $experimentProperty = $report.PSObject.Properties['experiment']
        $reportsProperty = $report.PSObject.Properties['repetition_reports']
        $scenarioProperty = $report.PSObject.Properties['scenario']
        $schedulerProperty = $report.PSObject.Properties['scheduler']
        $runtimeProperty = $report.PSObject.Properties['runtime']
        if ($null -eq $schemaProperty -or
            [string]$schemaProperty.Value -cne 'freeagent.s3-eval-report/v1' -or
            $null -eq $errorProperty -or
            -not ($errorProperty.Value -is [string]) -or
            $null -eq $experimentProperty -or
            $null -eq $reportsProperty -or
            $null -eq $scenarioProperty -or
            $null -eq $schedulerProperty -or
            $null -eq $runtimeProperty -or
            -not ($experimentProperty.Value -is [pscustomobject]) -or
            -not ($scenarioProperty.Value -is [pscustomobject]) -or
            -not ($schedulerProperty.Value -is [pscustomobject]) -or
            -not ($runtimeProperty.Value -is [pscustomobject])) {
            return $false
        }
        $experimentIdProperty = $experimentProperty.Value.PSObject.Properties['id']
        $requestedProperty = $experimentProperty.Value.PSObject.Properties[
            'repetitions_requested'
        ]
        $attemptedProperty = $experimentProperty.Value.PSObject.Properties[
            'repetitions_attempted'
        ]
        if ($null -eq $experimentIdProperty -or
            [string]$experimentIdProperty.Value -cne $ExpectedExperimentId -or
            $null -eq $requestedProperty -or $null -eq $attemptedProperty) {
            return $false
        }
        $scenarioSchema = $scenarioProperty.Value.PSObject.Properties[
            'schema_version'
        ]
        $scenarioExperiment = $scenarioProperty.Value.PSObject.Properties[
            'experiment_id'
        ]
        $scenarioTasks = $scenarioProperty.Value.PSObject.Properties['tasks']
        if ($null -eq $scenarioSchema -or
            [string]$scenarioSchema.Value -cne 'freeagent.s3-eval-scenario/v1' -or
            $null -eq $scenarioExperiment -or
            [string]$scenarioExperiment.Value -cne $ExpectedExperimentId -or
            $null -eq $scenarioTasks -or @($scenarioTasks.Value).Count -ne 3) {
            return $false
        }
        $deepSeekEnabled = $runtimeProperty.Value.PSObject.Properties[
            'deepseek_enabled'
        ]
        if ($null -eq $deepSeekEnabled -or
            -not [bool]$deepSeekEnabled.Value) {
            return $false
        }
        $schedulerEnabled = $schedulerProperty.Value.PSObject.Properties['enabled']
        $schedulerGlobal = $schedulerProperty.Value.PSObject.Properties[
            'global_workers'
        ]
        $schedulerWorkspace = $schedulerProperty.Value.PSObject.Properties[
            'workspace_workers'
        ]
        $schedulerFamily = $schedulerProperty.Value.PSObject.Properties[
            'composite_family_workers'
        ]
        if ($null -eq $schedulerEnabled -or $null -eq $schedulerGlobal -or
            $null -eq $schedulerWorkspace -or $null -eq $schedulerFamily) {
            return $false
        }
        $expectedEnabled = $ExpectedSchedulerMode -ceq 'ON'
        if ([bool]$schedulerEnabled.Value -ne $expectedEnabled -or
            [uint64]$schedulerGlobal.Value -ne $ExpectedGlobalWorkers -or
            [uint64]$schedulerWorkspace.Value -ne $ExpectedWorkspaceWorkers -or
            [uint64]$schedulerFamily.Value -ne $ExpectedFamilyWorkers) {
            return $false
        }
        [uint64]$requested = $requestedProperty.Value
        [uint64]$attempted = $attemptedProperty.Value
        if ($requested -ne [uint64]$ExpectedRepetitions -or
            $attempted -gt $requested -or
            @($reportsProperty.Value).Count -ne [int]$attempted) {
            return $false
        }
        $firstErrorEmpty = [string]::IsNullOrWhiteSpace(
            [string]$errorProperty.Value
        )
        if (($ExitCode -eq 0) -ne $firstErrorEmpty) { return $false }
        if ($ExitCode -eq 0 -and $attempted -ne $requested) { return $false }
        $repetitionReports = @($reportsProperty.Value)
        for ($index = 0; $index -lt $repetitionReports.Count; $index++) {
            $item = $repetitionReports[$index]
            if (-not ($item -is [pscustomobject])) { return $false }
            $number = $item.PSObject.Properties['repetition']
            $inner = $item.PSObject.Properties['report']
            $costs = $item.PSObject.Properties['derived_cost_estimates']
            $itemError = $item.PSObject.Properties['error']
            if ($null -eq $number -or [uint64]$number.Value -ne [uint64]($index + 1) -or
                $null -eq $inner -or -not ($inner.Value -is [pscustomobject]) -or
                $null -eq $costs -or $null -eq $itemError -or
                -not ($itemError.Value -is [string])) {
                return $false
            }
            $families = $inner.Value.PSObject.Properties['families']
            $serviceOrder = $inner.Value.PSObject.Properties['service_order']
            $fairness = $inner.Value.PSObject.Properties['fairness']
            if ($null -eq $families -or @($families.Value).Count -ne 3 -or
                $null -eq $serviceOrder -or $null -eq $fairness -or
                -not ($fairness.Value -is [pscustomobject])) {
                return $false
            }
            if ($ExitCode -eq 0 -and
                -not [string]::IsNullOrWhiteSpace([string]$itemError.Value)) {
                return $false
            }
        }
        return $true
    } catch {
        return $false
    }
}

function Get-S3CFileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = $null
    $sha = $null
    try {
        $stream = [IO.File]::Open(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-S3CHex -Bytes $sha.ComputeHash($stream)
    } catch {
        Fail-S3CRealCell -Code 'EVIDENCE_HASH_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Move-S3CCreateNew {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    try {
        [IO.File]::Move($Source, $Destination)
        [IO.File]::SetAttributes(
            $Destination,
            ([IO.File]::GetAttributes($Destination) -bor
                [IO.FileAttributes]::ReadOnly)
        )
    } catch {
        Fail-S3CRealCell -Code 'EVIDENCE_COMMIT_FAILED'
    }
}

function Remove-S3CPendingChecked {
    param([AllowNull()][string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return $true }
    try {
        if ([IO.File]::Exists($Path)) { [IO.File]::Delete($Path) }
        return -not [IO.File]::Exists($Path)
    } catch {
        return $false
    }
}

function Invoke-S3CCapturedPhase {
    param(
        [Parameter(Mandatory = $true)][string]$Phase,
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$JsonDestination,
        [Parameter(Mandatory = $true)][string]$IncompleteDestination,
        [Parameter(Mandatory = $true)][string]$ErrorDestination,
        [Parameter(Mandatory = $true)][string]$Secret,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds,
        [switch]$S3EvalReport,
        [int]$ExpectedRepetitions = 0,
        [string]$ExpectedExperimentId = '',
        [string]$ExpectedSchedulerMode = 'OFF',
        [uint64]$ExpectedGlobalWorkers = 0,
        [uint64]$ExpectedWorkspaceWorkers = 0,
        [uint64]$ExpectedFamilyWorkers = 0
    )
    $nonce = [Guid]::NewGuid().ToString('N')
    $stdoutPending = Join-Path $script:CellPath (
        ".$Phase.stdout.pending.$nonce"
    )
    $stderrPending = Join-Path $script:CellPath (
        ".$Phase.stderr.pending.$nonce"
    )
    $stdoutStream = $null
    $stderrStream = $null
    $process = $null
    $jobHandle = [IntPtr]::Zero
    $phaseFailed = $false
    $timedOut = $false
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
        $start.Arguments = Join-S3CWindowsArguments -Values $Arguments
        $start.WorkingDirectory = $WorkingDirectory
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) {
            Fail-S3CRealCell -Code 'PROCESS_START_FAILED'
        }
        $jobHandle = [S3CProcessJob]::Create($process)
        $stdoutTask = $process.StandardOutput.BaseStream.CopyToAsync($stdoutStream)
        $stderrTask = $process.StandardError.BaseStream.CopyToAsync($stderrStream)
        $timedOut = -not $process.WaitForExit($TimeoutMilliseconds)
        if ($timedOut) {
            [S3CProcessJob]::Terminate($jobHandle)
            if (-not $process.WaitForExit(30000)) {
                throw 'bounded process termination failed'
            }
        }
        [S3CProcessJob]::Close($jobHandle)
        $jobHandle = [IntPtr]::Zero
        if (-not $stdoutTask.Wait(30000)) {
            throw 'bounded stdout drain failed'
        }
        if (-not $stderrTask.Wait(30000)) {
            throw 'bounded stderr drain failed'
        }
        $stdoutStream.Flush($true)
        $stderrStream.Flush($true)
        $exitCode = if ($timedOut) { 124 } else { [int]$process.ExitCode }
    } catch {
        $script:FailureExceptionType = $_.Exception.GetType().FullName
        $phaseFailed = $true
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [S3CProcessJob]::Terminate($jobHandle) } catch {}
            try { [S3CProcessJob]::Close($jobHandle) } catch {}
            $jobHandle = [IntPtr]::Zero
        } elseif ($null -ne $process) {
            try {
                if (-not $process.HasExited) { $process.Kill() }
            } catch {}
        }
    } finally {
        if ($null -ne $stdoutStream) { $stdoutStream.Dispose() }
        if ($null -ne $stderrStream) { $stderrStream.Dispose() }
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [S3CProcessJob]::Close($jobHandle) } catch {}
        }
        if ($null -ne $process) { $process.Dispose() }
    }
    if ($phaseFailed) {
        $stdoutRemoved = Remove-S3CPendingChecked -Path $stdoutPending
        $stderrRemoved = Remove-S3CPendingChecked -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = 125
            JsonValid = $false
            LeakDetected = $false
            TimedOut = [bool]$timedOut
            CaptureFailed = $true
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    try {
        [byte[]]$stdoutBytes = [IO.File]::ReadAllBytes($stdoutPending)
        [byte[]]$stderrBytes = [IO.File]::ReadAllBytes($stderrPending)
    } catch {
        $stdoutRemoved = Remove-S3CPendingChecked -Path $stdoutPending
        $stderrRemoved = Remove-S3CPendingChecked -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = 125
            JsonValid = $false
            LeakDetected = $false
            TimedOut = [bool]$timedOut
            CaptureFailed = $true
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    $leak = (Test-S3CCaptureLeak -Bytes $stdoutBytes -Secret $Secret) -or
        (Test-S3CCaptureLeak -Bytes $stderrBytes -Secret $Secret)
    if ($leak) {
        $stdoutRemoved = Remove-S3CPendingChecked -Path $stdoutPending
        $stderrRemoved = Remove-S3CPendingChecked -Path $stderrPending
        return [pscustomobject]@{
            ExitCode = $exitCode
            JsonValid = $false
            LeakDetected = $true
            TimedOut = [bool]$timedOut
            CaptureFailed = $false
            CleanupFailed = -not ($stdoutRemoved -and $stderrRemoved)
        }
    }
    $jsonValid = if ($S3EvalReport) {
        Test-S3CEvalReportCapture `
            -Bytes $stdoutBytes `
            -ExitCode $exitCode `
            -ExpectedRepetitions $ExpectedRepetitions `
            -ExpectedExperimentId $ExpectedExperimentId `
            -ExpectedSchedulerMode $ExpectedSchedulerMode `
            -ExpectedGlobalWorkers $ExpectedGlobalWorkers `
            -ExpectedWorkspaceWorkers $ExpectedWorkspaceWorkers `
            -ExpectedFamilyWorkers $ExpectedFamilyWorkers
    } else {
        Test-S3CJsonCapture -Bytes $stdoutBytes
    }
    if ($jsonValid) {
        Move-S3CCreateNew -Source $stdoutPending -Destination $JsonDestination
    } else {
        Move-S3CCreateNew `
            -Source $stdoutPending `
            -Destination $IncompleteDestination
    }
    Move-S3CCreateNew -Source $stderrPending -Destination $ErrorDestination
    return [pscustomobject]@{
        ExitCode = $exitCode
        JsonValid = [bool]$jsonValid
        LeakDetected = $false
        TimedOut = [bool]$timedOut
        CaptureFailed = $false
        CleanupFailed = $false
    }
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    [Console]::Error.WriteLine('S3C_REAL_CELL_FAIL code=WINDOWS_REQUIRED')
    exit 1
}

if ($null -eq ('S3CExclusiveDirectory' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

public static class S3CExclusiveDirectory
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

public static class S3CByteScanner
{
    public static bool Contains(
        byte[] haystack,
        byte[] needle,
        bool asciiIgnoreCase)
    {
        if (haystack == null || needle == null || needle.Length == 0 ||
            haystack.Length < needle.Length)
            return false;

        int last = haystack.Length - needle.Length;
        for (int offset = 0; offset <= last; offset++)
        {
            bool matches = true;
            for (int index = 0; index < needle.Length; index++)
            {
                byte left = haystack[offset + index];
                byte right = needle[index];
                if (asciiIgnoreCase)
                {
                    left = FoldAscii(left);
                    right = FoldAscii(right);
                }
                if (left != right)
                {
                    matches = false;
                    break;
                }
            }
            if (matches) return true;
        }
        return false;
    }

    private static byte FoldAscii(byte value)
    {
        return value >= 97 && value <= 122
            ? (byte)(value - 32)
            : value;
    }
}

public static class S3CProcessJob
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
        IntPtr job,
        UInt32 informationClass,
        IntPtr information,
        UInt32 informationLength);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

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
                job,
                ExtendedLimitInformation,
                information,
                (UInt32)size))
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
            'S3C_REAL_CELL_FAIL code=EXCLUSIVE_DIRECTORY_RUNTIME_UNAVAILABLE'
        )
        exit 1
    }
}

$startedAt = [DateTimeOffset]::UtcNow
$status = 'HARNESS_ERROR'
$processExit = 1
$reportCommitted = $false
$archiveVerified = $false
$initExit = $null
$evalExit = $null
$backupExit = $null
$verifyExit = $null
$reportSha256 = $null
$initTimedOut = $false
$evalTimedOut = $false
$backupTimedOut = $false
$verifyTimedOut = $false
$initCaptureFailed = $false
$evalCaptureFailed = $false
$backupCaptureFailed = $false
$verifyCaptureFailed = $false
$terminalFailureCode = ''
$sensitiveCaptureCleanupFailed = $false

try {
    if ($CellId -cnotmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$') {
        Fail-S3CRealCell -Code 'CELL_ID_INVALID'
    }
    if ($SecretEnvironmentName -cnotmatch '^[A-Za-z_][A-Za-z0-9_]{0,127}$') {
        Fail-S3CRealCell -Code 'SECRET_ENVIRONMENT_NAME_INVALID'
    }
    $secret = [Environment]::GetEnvironmentVariable($SecretEnvironmentName, 'Process')
    if ([string]::IsNullOrWhiteSpace($secret)) {
        Fail-S3CRealCell -Code 'SECRET_UNAVAILABLE'
    }
    $schedulerModeNormalized = $SchedulerMode.ToUpperInvariant()
    if ($schedulerModeNormalized -ceq 'OFF') {
        if ($SchedulerGlobalWorkers -ne 0 -or
            $SchedulerWorkspaceWorkers -ne 0 -or
            $SchedulerFamilyWorkers -ne 0) {
            Fail-S3CRealCell -Code 'SCHEDULER_CONFIGURATION_INVALID'
        }
    } elseif ($schedulerModeNormalized -ceq 'ON') {
        if ($SchedulerGlobalWorkers -eq 0 -or
            $SchedulerWorkspaceWorkers -eq 0 -or
            $SchedulerFamilyWorkers -eq 0 -or
            $SchedulerWorkspaceWorkers -gt $SchedulerGlobalWorkers -or
            $SchedulerFamilyWorkers -gt $SchedulerGlobalWorkers) {
            Fail-S3CRealCell -Code 'SCHEDULER_CONFIGURATION_INVALID'
        }
    } else {
        Fail-S3CRealCell -Code 'SCHEDULER_CONFIGURATION_INVALID'
    }

    $binarySource = Get-S3CFullPath `
        -Value $FreeAgentPath `
        -Code 'FREEAGENT_BINARY_INVALID'
    $seedSource = Get-S3CFullPath -Value $SeedPath -Code 'SEED_INVALID'
    $scenarioSource = Get-S3CFullPath `
        -Value $ScenarioPath `
        -Code 'SCENARIO_INVALID'
    $output = Get-S3CFullPath -Value $OutputRoot -Code 'OUTPUT_ROOT_INVALID'
    Assert-S3CRegularFile `
        -Path $binarySource `
        -Code 'FREEAGENT_BINARY_INVALID'
    Assert-S3CRegularFile -Path $seedSource -Code 'SEED_INVALID'
    Assert-S3CRegularFile -Path $scenarioSource -Code 'SCENARIO_INVALID'
    $verifiedSourceFiles = New-Object `
        'Collections.Generic.HashSet[string]' `
        ([StringComparer]::OrdinalIgnoreCase)
    [uint64]$verifiedSourceBytes = 0
    foreach ($sourcePath in @($binarySource, $seedSource, $scenarioSource)) {
        if ($verifiedSourceFiles.Add($sourcePath)) {
            $verifiedSourceBytes = Add-S3CCheckedFileLength `
                -Current $verifiedSourceBytes `
                -File (Get-Item -LiteralPath $sourcePath -Force)
        }
    }
    $seedObject = Read-S3CJsonObject -Path $seedSource -Code 'SEED_INVALID'
    $scenarioObject = Read-S3CJsonObject `
        -Path $scenarioSource `
        -Code 'SCENARIO_INVALID'
    $experimentIdProperty = $scenarioObject.PSObject.Properties['experiment_id']
    if ($null -eq $experimentIdProperty -or
        [string]::IsNullOrWhiteSpace([string]$experimentIdProperty.Value)) {
        Fail-S3CRealCell -Code 'SCENARIO_INVALID'
    }
    $seedDirectory = [IO.Path]::GetDirectoryName($seedSource)
    $artifactSet = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $artifactReferences = New-Object 'Collections.Generic.List[object]'
    foreach ($reference in @(Find-S3CArtifactRelativePath -Node $seedObject)) {
        $resolvedReference = Resolve-S3CArtifactPath `
            -SeedDirectory $seedDirectory `
            -RelativePath ([string]$reference)
        if ($artifactSet.Add($resolvedReference.RelativePath)) {
            $artifactReferences.Add($resolvedReference)
        }
    }
    foreach ($sourcePath in @($binarySource, $seedSource, $scenarioSource)) {
        Assert-S3CSourceHasNoSecret -Path $sourcePath -Secret $secret
    }
    $scannedArtifactFiles = New-Object `
        'Collections.Generic.HashSet[string]' `
        ([StringComparer]::OrdinalIgnoreCase)
    foreach ($artifact in $artifactReferences) {
        foreach ($sourceFile in @(Get-S3CSafeTreeFiles -Root $artifact.SourcePath)) {
            if ($scannedArtifactFiles.Add($sourceFile.FullName)) {
                Assert-S3CSourceHasNoSecret `
                    -Path $sourceFile.FullName `
                    -Secret $secret
                if ($verifiedSourceFiles.Add($sourceFile.FullName)) {
                    $verifiedSourceBytes = Add-S3CCheckedFileLength `
                        -Current $verifiedSourceBytes `
                        -File $sourceFile
                }
            }
        }
    }
    $futureCellPath = Join-Path $output $CellId
    $overlapSources = New-Object 'Collections.Generic.List[object]'
    foreach ($sourcePath in @($binarySource, $seedSource, $scenarioSource)) {
        $overlapSources.Add($sourcePath)
    }
    foreach ($artifact in $artifactReferences) {
        $overlapSources.Add($artifact.SourcePath)
    }
    Assert-S3CPathsDoNotOverlapCell `
        -OutputRootPath $output `
        -FutureCellPath $futureCellPath `
        -SourcePaths $overlapSources.ToArray()

    if (Test-Path -LiteralPath $output) {
        if (-not (Test-Path -LiteralPath $output -PathType Container)) {
            Fail-S3CRealCell -Code 'OUTPUT_ROOT_INVALID'
        }
    } else {
        try {
            [void][IO.Directory]::CreateDirectory($output)
        } catch {
            Fail-S3CRealCell -Code 'OUTPUT_ROOT_INVALID'
        }
    }
    Assert-S3CNoReparseAncestry -Path $output -Code 'OUTPUT_ROOT_UNSAFE'
    Assert-S3COutputCapacity `
        -OutputRootPath $output `
        -VerifiedSourceBytes $verifiedSourceBytes
    $script:CellPath = $futureCellPath
    $createCode = [S3CExclusiveDirectory]::Create($script:CellPath)
    if ($createCode -eq 183 -or $createCode -eq 80) {
        Fail-S3CRealCell -Code 'CELL_COLLISION'
    }
    if ($createCode -ne 0) {
        Fail-S3CRealCell -Code 'CELL_CREATE_FAILED'
    }
    $script:CellCreated = $true

    $inputRoot = Join-Path $script:CellPath 'inputs'
    $binaryRoot = Join-Path $inputRoot 'bin'
    $frozenSeedRoot = Join-Path $inputRoot 'seed-root'
    $workRoot = Join-Path $script:CellPath 'work'
    $archiveRoot = Join-Path $script:CellPath 'archive'
    foreach ($directory in @(
        $inputRoot, $binaryRoot, $frozenSeedRoot, $workRoot, $archiveRoot
    )) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    $frozenBinary = Join-Path $binaryRoot ([IO.Path]::GetFileName($binarySource))
    $frozenSeed = Join-Path $frozenSeedRoot 'seed.json'
    $frozenScenario = Join-Path $inputRoot 'scenario.json'
    $binaryLock = Copy-S3CSnapshotFile `
        -Source $binarySource `
        -Destination $frozenBinary `
        -Secret $secret
    $seedLock = Copy-S3CSnapshotFile `
        -Source $seedSource `
        -Destination $frozenSeed `
        -Secret $secret
    $scenarioLock = Copy-S3CSnapshotFile `
        -Source $scenarioSource `
        -Destination $frozenScenario `
        -Secret $secret

    $frozenSeedObject = Read-S3CJsonObject `
        -Path $frozenSeed `
        -Code 'FROZEN_SEED_INVALID'
    $frozenReferenceSet = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($reference in @(
        Find-S3CArtifactRelativePath -Node $frozenSeedObject
    )) {
        $resolved = Resolve-S3CArtifactPath `
            -SeedDirectory $seedDirectory `
            -RelativePath ([string]$reference)
        [void]$frozenReferenceSet.Add($resolved.RelativePath)
    }
    if ($frozenReferenceSet.Count -ne $artifactSet.Count) {
        Fail-S3CRealCell -Code 'SEED_CHANGED_DURING_SNAPSHOT'
    }
    foreach ($reference in $artifactSet) {
        if (-not $frozenReferenceSet.Contains($reference)) {
            Fail-S3CRealCell -Code 'SEED_CHANGED_DURING_SNAPSHOT'
        }
    }

    $artifactLocks = New-Object 'Collections.Generic.List[object]'
    $destinationSet = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($artifact in $artifactReferences) {
        $frozenArtifactPath = Join-Path $frozenSeedRoot $artifact.RelativePath
        $sourceItem = Get-Item -LiteralPath $artifact.SourcePath -Force
        if ($sourceItem -is [IO.DirectoryInfo]) {
            [void][IO.Directory]::CreateDirectory($frozenArtifactPath)
        }
        foreach ($sourceFile in @(Get-S3CSafeTreeFiles -Root $artifact.SourcePath)) {
            $sourceRelative = Get-S3CRelativePath `
                -Parent $seedDirectory `
                -Child $sourceFile.FullName
            if (-not $destinationSet.Add($sourceRelative)) { continue }
            $frozenArtifactFile = Join-Path $frozenSeedRoot $sourceRelative
            $lock = Copy-S3CSnapshotFile `
                -Source $sourceFile.FullName `
                -Destination $frozenArtifactFile `
                -Secret $secret
            $artifactLocks.Add([ordered]@{
                relative_path = $sourceRelative.Replace('\', '/')
                size_bytes = $lock.SizeBytes
                sha256 = $lock.Sha256
            })
        }
    }
    $sortedArtifactLocks = @($artifactLocks | Sort-Object relative_path)
    $inputLocks = [ordered]@{
        schema_version = 'freeagent.s3c-input-locks/v1'
        binary = [ordered]@{
            relative_path = ('inputs/bin/' + [IO.Path]::GetFileName($frozenBinary))
            size_bytes = $binaryLock.SizeBytes
            sha256 = $binaryLock.Sha256
        }
        seed = [ordered]@{
            relative_path = 'inputs/seed-root/seed.json'
            size_bytes = $seedLock.SizeBytes
            sha256 = $seedLock.Sha256
        }
        scenario = [ordered]@{
            relative_path = 'inputs/scenario.json'
            size_bytes = $scenarioLock.SizeBytes
            sha256 = $scenarioLock.Sha256
        }
        artifacts = $sortedArtifactLocks
    }
    Write-S3CCreateNewJson `
        -Path (Join-Path $script:CellPath 'input-locks.json') `
        -Value $inputLocks `
        -ReadOnly
    $plan = [ordered]@{
        schema_version = 'freeagent.s3c-cell-plan/v1'
        cell_id = $CellId
        created_at_utc = $startedAt.ToString('O')
        scheduler = [ordered]@{
            mode = $schedulerModeNormalized
            global_workers = $SchedulerGlobalWorkers
            workspace_workers = $SchedulerWorkspaceWorkers
            family_workers = $SchedulerFamilyWorkers
        }
        repetitions = $Repetitions
        secret_environment_name = $SecretEnvironmentName
        automatic_retry = $false
        paths = [ordered]@{
            database = 'work/current.sqlite'
            artifact_root = 'work/artifacts'
            backup_bundle = 'archive/store.bundle'
        }
    }
    Write-S3CCreateNewJson `
        -Path (Join-Path $script:CellPath 'cell-plan.json') `
        -Value $plan `
        -ReadOnly

    $databasePath = Join-Path $workRoot 'current.sqlite'
    $artifactRoot = Join-Path $workRoot 'artifacts'
    $bundlePath = Join-Path $archiveRoot 'store.bundle'
    $initResult = Invoke-S3CCapturedPhase `
        -Phase 'init' `
        -Executable $frozenBinary `
        -Arguments @(
            'init', '--db', $databasePath, '--seed', $frozenSeed,
            '--artifact-root', $artifactRoot
        ) `
        -WorkingDirectory $script:CellPath `
        -JsonDestination (Join-Path $script:CellPath 'init.json') `
        -IncompleteDestination (Join-Path $script:CellPath 'init.stdout.incomplete') `
        -ErrorDestination (Join-Path $script:CellPath 'init.stderr.txt') `
        -Secret $secret `
        -TimeoutMilliseconds $script:OperationalTimeoutMilliseconds
    $initExit = $initResult.ExitCode
    $initTimedOut = $initResult.TimedOut
    $initCaptureFailed = $initResult.CaptureFailed
    $sensitiveCaptureCleanupFailed = $initResult.CleanupFailed
    if ($sensitiveCaptureCleanupFailed) {
        $status = 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
        Fail-S3CRealCell -Code 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
    }
    if ($initResult.LeakDetected) {
        $status = 'SECRET_LEAK_BLOCKED'
        Fail-S3CRealCell -Code 'SECRET_LEAK_BLOCKED'
    }
    if ($initExit -ne 0 -or -not $initResult.JsonValid) {
        $status = 'INIT_FAILED'
        Fail-S3CRealCell -Code 'INIT_FAILED'
    }

    $evalArguments = New-Object 'Collections.Generic.List[string]'
    foreach ($argument in @(
        's3-eval', '--db', $databasePath,
        '--artifact-root', $artifactRoot,
        '--scenario', $frozenScenario,
        '--repetitions', [string]$Repetitions,
        '--enable-deepseek',
        '--deepseek-api-key-env', $SecretEnvironmentName
    )) {
        $evalArguments.Add([string]$argument)
    }
    if ($schedulerModeNormalized -ceq 'ON') {
        foreach ($argument in @(
            '--enable-fair-scheduler',
            '--scheduler-global-workers', [string]$SchedulerGlobalWorkers,
            '--scheduler-workspace-workers', [string]$SchedulerWorkspaceWorkers,
            '--scheduler-family-workers', [string]$SchedulerFamilyWorkers
        )) {
            $evalArguments.Add([string]$argument)
        }
    }
    $evalResult = Invoke-S3CCapturedPhase `
        -Phase 's3-eval' `
        -Executable $frozenBinary `
        -Arguments $evalArguments.ToArray() `
        -WorkingDirectory $script:CellPath `
        -JsonDestination (Join-Path $script:CellPath 'report.json') `
        -IncompleteDestination (Join-Path $script:CellPath 'report.stdout.incomplete') `
        -ErrorDestination (Join-Path $script:CellPath 's3-eval.stderr.txt') `
        -Secret $secret `
        -TimeoutMilliseconds $script:EvaluationTimeoutMilliseconds `
        -S3EvalReport `
        -ExpectedRepetitions $Repetitions `
        -ExpectedExperimentId ([string]$experimentIdProperty.Value) `
        -ExpectedSchedulerMode $schedulerModeNormalized `
        -ExpectedGlobalWorkers $SchedulerGlobalWorkers `
        -ExpectedWorkspaceWorkers $SchedulerWorkspaceWorkers `
        -ExpectedFamilyWorkers $SchedulerFamilyWorkers
    $evalExit = $evalResult.ExitCode
    $evalTimedOut = $evalResult.TimedOut
    $evalCaptureFailed = $evalResult.CaptureFailed
    $sensitiveCaptureCleanupFailed = $evalResult.CleanupFailed
    if ($sensitiveCaptureCleanupFailed) {
        $status = 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
        Fail-S3CRealCell -Code 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
    }
    if ($evalResult.LeakDetected) {
        $status = 'SECRET_LEAK_BLOCKED'
        Fail-S3CRealCell -Code 'SECRET_LEAK_BLOCKED'
    }
    $reportCommitted = [bool]$evalResult.JsonValid
    if ($reportCommitted) {
        $reportSha256 = Get-S3CFileSha256 `
            -Path (Join-Path $script:CellPath 'report.json')
    }

    $backupResult = Invoke-S3CCapturedPhase `
        -Phase 'backup' `
        -Executable $frozenBinary `
        -Arguments @(
            'backup', '--db', $databasePath,
            '--artifact-root', $artifactRoot,
            '--out', $bundlePath
        ) `
        -WorkingDirectory $script:CellPath `
        -JsonDestination (Join-Path $script:CellPath 'backup.json') `
        -IncompleteDestination (Join-Path $script:CellPath 'backup.stdout.incomplete') `
        -ErrorDestination (Join-Path $script:CellPath 'backup.stderr.txt') `
        -Secret $secret `
        -TimeoutMilliseconds $script:OperationalTimeoutMilliseconds
    $backupExit = $backupResult.ExitCode
    $backupTimedOut = $backupResult.TimedOut
    $backupCaptureFailed = $backupResult.CaptureFailed
    $sensitiveCaptureCleanupFailed = $backupResult.CleanupFailed
    if ($sensitiveCaptureCleanupFailed) {
        $status = 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
        Fail-S3CRealCell -Code 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
    }
    if ($backupResult.LeakDetected) {
        $status = 'SECRET_LEAK_BLOCKED'
        Fail-S3CRealCell -Code 'SECRET_LEAK_BLOCKED'
    }
    if ($backupExit -ne 0 -or -not $backupResult.JsonValid) {
        $status = 'ARCHIVE_FAILED'
        Fail-S3CRealCell -Code 'ARCHIVE_FAILED'
    }

    $verifyResult = Invoke-S3CCapturedPhase `
        -Phase 'backup-verify' `
        -Executable $frozenBinary `
        -Arguments @('backup-verify', '--bundle', $bundlePath) `
        -WorkingDirectory $script:CellPath `
        -JsonDestination (Join-Path $script:CellPath 'backup-verify.json') `
        -IncompleteDestination (Join-Path $script:CellPath 'backup-verify.stdout.incomplete') `
        -ErrorDestination (Join-Path $script:CellPath 'backup-verify.stderr.txt') `
        -Secret $secret `
        -TimeoutMilliseconds $script:OperationalTimeoutMilliseconds
    $verifyExit = $verifyResult.ExitCode
    $verifyTimedOut = $verifyResult.TimedOut
    $verifyCaptureFailed = $verifyResult.CaptureFailed
    $sensitiveCaptureCleanupFailed = $verifyResult.CleanupFailed
    if ($sensitiveCaptureCleanupFailed) {
        $status = 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
        Fail-S3CRealCell -Code 'SENSITIVE_CAPTURE_CLEANUP_FAILED'
    }
    if ($verifyResult.LeakDetected) {
        $status = 'SECRET_LEAK_BLOCKED'
        Fail-S3CRealCell -Code 'SECRET_LEAK_BLOCKED'
    }
    if ($verifyExit -ne 0 -or -not $verifyResult.JsonValid) {
        $status = 'ARCHIVE_FAILED'
        Fail-S3CRealCell -Code 'ARCHIVE_FAILED'
    }
    $archiveVerified = $true

    if (-not $reportCommitted) {
        $status = 'NO_VALID_REPORT'
        $terminalFailureCode = if ($evalCaptureFailed) {
            'S3_EVAL_CAPTURE_FAILED'
        } else {
            'S3_EVAL_REPORT_INVALID'
        }
        $processExit = 2
    } elseif ($evalExit -ne 0) {
        $status = 'PARTIAL'
        $processExit = 2
    } else {
        $status = 'COMPLETE'
        $processExit = 0
    }
} catch {
    if (-not $script:CellCreated) {
        [Console]::Error.WriteLine(
            'S3C_REAL_CELL_FAIL code=' + $script:FailureCode
        )
        exit 1
    }
    if ([string]::IsNullOrWhiteSpace($script:FailureExceptionType)) {
        $script:FailureExceptionType = $_.Exception.GetType().FullName
    }
    if ($script:FailureCode -ceq 'UNCLASSIFIED') {
        $script:FailureCode = 'HARNESS_UNEXPECTED'
    }
    if ($status -ceq 'HARNESS_ERROR') {
        if ($script:FailureCode -ceq 'SECRET_LEAK_BLOCKED') {
            $status = 'SECRET_LEAK_BLOCKED'
        } else {
            $status = 'HARNESS_ERROR'
        }
    }
    $processExit = 2
}

if ($script:CellCreated) {
    $finishedAt = [DateTimeOffset]::UtcNow
    $exitRecord = [ordered]@{
        schema_version = 'freeagent.s3c-real-cell-exit/v1'
        cell_id = $CellId
        started_at_utc = $startedAt.ToString('O')
        finished_at_utc = $finishedAt.ToString('O')
        status = $status
        failure_code = $(if (-not [string]::IsNullOrWhiteSpace(
            $terminalFailureCode
        )) {
            $terminalFailureCode
        } elseif ($status -ceq 'COMPLETE' -or $status -ceq 'PARTIAL') {
            ''
        } else {
            $script:FailureCode
        })
        failure_exception_type = $script:FailureExceptionType
        phases = [ordered]@{
            init_exit_code = $initExit
            init_timed_out = [bool]$initTimedOut
            init_capture_failed = [bool]$initCaptureFailed
            s3_eval_exit_code = $evalExit
            s3_eval_timed_out = [bool]$evalTimedOut
            s3_eval_capture_failed = [bool]$evalCaptureFailed
            backup_exit_code = $backupExit
            backup_timed_out = [bool]$backupTimedOut
            backup_capture_failed = [bool]$backupCaptureFailed
            backup_verify_exit_code = $verifyExit
            backup_verify_timed_out = [bool]$verifyTimedOut
            backup_verify_capture_failed = [bool]$verifyCaptureFailed
        }
        report_committed = [bool]$reportCommitted
        report_sha256 = $reportSha256
        archive_verified = [bool]$archiveVerified
        automatic_retry = $false
        manual_review_required = ($status -cne 'COMPLETE')
        raw_work_retained = $true
        sensitive_capture_cleanup_failed = [bool]$sensitiveCaptureCleanupFailed
    }
    try {
        Write-S3CAtomicExitJson `
            -CellPath $script:CellPath `
            -Value $exitRecord
        $script:ExitWritten = $true
    } catch {
        [Console]::Error.WriteLine(
            'S3C_REAL_CELL_FAIL code=EXIT_RECORD_WRITE_FAILED'
        )
        exit 1
    }
    [Console]::Out.WriteLine(
        'S3C_REAL_CELL status=' + $status + ' cell_id=' + $CellId
    )
    if ($status -cne 'COMPLETE') {
        [Console]::Error.WriteLine(
            'S3C_REAL_CELL_REVIEW_REQUIRED code=' + $status
        )
    }
}

exit $processExit
