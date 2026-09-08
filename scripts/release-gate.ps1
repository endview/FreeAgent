[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ArtifactRoot,
    [Parameter(Mandatory = $true)][string]$ManifestSha256,
    [Parameter(Mandatory = $true)][string]$GoCommand,
    [string]$RaceTestTimeout = '30m',
    [switch]$SkipRace
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$originalLocation = Get-Location
$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:IsMacOSPlatform = $false
if (-not $script:IsWindowsPlatform) {
    try {
        $script:IsMacOSPlatform =
            [Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
                [Runtime.InteropServices.OSPlatform]::OSX
            )
    } catch {
        $script:IsMacOSPlatform =
            [Environment]::OSVersion.Platform -eq [PlatformID]::MacOSX
    }
}
$script:ManifestName = 'public-tree-manifest.v1.json'
$script:EnvironmentNames = @(
    'CGO_ENABLED', 'GOOS', 'GOARCH', 'CC',
    'GOCACHE', 'GOMODCACHE', 'GOTMPDIR',
    'TEMP', 'TMP', 'TMPDIR',
    'GOWORK', 'GOFLAGS', 'GOENV', 'GOTOOLCHAIN', 'GOPATH'
)
$savedEnvironment = @{}
foreach ($environmentName in $script:EnvironmentNames) {
    $savedEnvironment[$environmentName] =
        [Environment]::GetEnvironmentVariable($environmentName, 'Process')
}

function Fail-ReleaseGate {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "RELEASE_GATE_FAIL code=$Code"
}

function Resolve-ReleaseGateFailure {
    param(
        [bool]$PrimaryFailed = $false,
        [AllowNull()][string]$PrimaryMessage,
        [bool]$PostFailed = $false,
        [AllowNull()][string]$PostMessage,
        [AllowEmptyCollection()][string[]]$RestoreFailures = @()
    )

    $restoreDetails = @($RestoreFailures)
    $hasPrimary = $PrimaryFailed -or
        -not [string]::IsNullOrEmpty($PrimaryMessage)
    $hasPost = $PostFailed -or
        -not [string]::IsNullOrEmpty($PostMessage)
    $primaryDetail = if (-not $hasPrimary) {
        '<none>'
    } elseif ([string]::IsNullOrEmpty($PrimaryMessage)) {
        '<empty>'
    } else {
        $PrimaryMessage
    }
    $postDetail = if ([string]::IsNullOrEmpty($PostMessage)) {
        '<empty>'
    } else {
        $PostMessage
    }
    if ($hasPost) {
        return (
            'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED ' +
            "primary=[$primaryDetail] post=[$postDetail] " +
            "restore=[$($restoreDetails -join '; ')]"
        )
    }
    if ($restoreDetails.Count -gt 0) {
        return (
            'RELEASE_GATE_FAIL code=RG_RESTORE_FAILED ' +
            "primary=[$primaryDetail] restore=[$($restoreDetails -join '; ')]"
        )
    }
    if ($hasPrimary) {
        if ([string]::IsNullOrEmpty($PrimaryMessage)) {
            return 'RELEASE_GATE_FAIL code=RG_PRIMARY_FAILED detail=[<empty>]'
        }
        return $PrimaryMessage
    }
    return $null
}

function Set-ProcessEnvironment {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()][string]$Value
    )
    [Environment]::SetEnvironmentVariable($Name, $Value, 'Process')
}

function Restore-Environment {
    foreach ($name in $script:EnvironmentNames) {
        Set-ProcessEnvironment -Name $name -Value $savedEnvironment[$name]
    }
}

function Get-NormalizedAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Value)

    $isAbsolute = $false
    if (-not [string]::IsNullOrWhiteSpace($Value)) {
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
    }
    if (-not $isAbsolute) {
        Fail-ReleaseGate -Code 'RG_PATH_NOT_ABSOLUTE'
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-ReleaseGate -Code 'RG_PATH_NOT_ABSOLUTE'
    }
    if ($script:IsWindowsPlatform -and (
        $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\??\', [StringComparison]::Ordinal)
    )) {
        Fail-ReleaseGate -Code 'RG_PATH_NOT_ABSOLUTE'
    }
    return $full.TrimEnd([char[]]@(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    ))
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
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            try {
                $item = Get-Item -LiteralPath $cursor -Force
            } catch {
                Fail-ReleaseGate -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-ReleaseGate -Code $Code
            }
        }
        $parent = [IO.Directory]::GetParent($cursor)
        if ($null -eq $parent) { break }
        $next = $parent.FullName
        if ([string]::Equals($next, $cursor, [StringComparison]::OrdinalIgnoreCase)) { break }
        $cursor = $next
    }
}

function Get-ReleaseGateSha256Hex {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes
    )

    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha256.ComputeHash($Bytes))).
            Replace('-', '').
            ToLowerInvariant()
    } finally {
        $sha256.Dispose()
    }
}

function Get-AuthenticatedStagingVerifier {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ManifestPin
    )

    Assert-NoReparseAncestry `
        -Path $ManifestPath `
        -Code 'RG_STAGING_MANIFEST_AUTH_FAILED'

    try {
        [byte[]]$manifestBytes = [IO.File]::ReadAllBytes($ManifestPath)
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_AUTH_FAILED'
    }
    if ((Get-ReleaseGateSha256Hex -Bytes $manifestBytes) -cne $ManifestPin) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_AUTH_FAILED'
    }

    if ($manifestBytes.Length -ge 3 -and
        $manifestBytes[0] -eq 0xEF -and
        $manifestBytes[1] -eq 0xBB -and
        $manifestBytes[2] -eq 0xBF) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }
    try {
        $strictUtf8 = New-Object Text.UTF8Encoding($false, $true)
        $manifestText = $strictUtf8.GetString($manifestBytes)
        $manifest = $manifestText | ConvertFrom-Json
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }

    if ($null -eq $manifest) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }
    $filesProperty = $manifest.PSObject.Properties['files']
    if ($null -eq $filesProperty -or
        $filesProperty.Value -isnot [System.Array]) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }
    $matchingRecords = New-Object 'System.Collections.Generic.List[object]'
    foreach ($record in $filesProperty.Value) {
        if ($null -eq $record) { continue }
        $pathProperty = $record.PSObject.Properties['path']
        if ($null -ne $pathProperty -and
            $pathProperty.Value -is [string] -and
            [string]$pathProperty.Value -ceq 'scripts/Test-PublicStaging.ps1') {
            $matchingRecords.Add($record)
        }
    }
    if ($matchingRecords.Count -ne 1) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }

    $verifierRecord = $matchingRecords[0]
    [string[]]$actualProperties = @(
        $verifierRecord.PSObject.Properties |
            ForEach-Object { $_.Name }
    )
    [string[]]$expectedProperties = @('path', 'sha256', 'size')
    [Array]::Sort($actualProperties, [StringComparer]::Ordinal)
    [Array]::Sort($expectedProperties, [StringComparer]::Ordinal)
    if (($actualProperties -join "`n") -cne ($expectedProperties -join "`n")) {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }

    $sizeValue = $verifierRecord.size
    $shaValue = $verifierRecord.sha256
    if (($sizeValue -isnot [int] -and $sizeValue -isnot [long]) -or
        [int64]$sizeValue -lt 0 -or
        $shaValue -isnot [string] -or
        [string]$shaValue -cnotmatch '^[0-9a-f]{64}$') {
        Fail-ReleaseGate -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
    }

    $verifier = Join-Path (Join-Path $Root 'scripts') 'Test-PublicStaging.ps1'
    if (-not (Test-Path -LiteralPath $verifier -PathType Leaf)) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }
    Assert-NoReparseAncestry `
        -Path $verifier `
        -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    try {
        $verifierItem = Get-Item -LiteralPath $verifier -Force
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }
    $linkType = $verifierItem.PSObject.Properties['LinkType']
    if (-not ($verifierItem -is [IO.FileInfo]) -or
        ($verifierItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        ($null -ne $linkType -and
            -not [string]::IsNullOrWhiteSpace([string]$linkType.Value))) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }
    if ($script:IsWindowsPlatform -and
        ($verifierItem.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }

    try {
        [byte[]]$verifierBytes = [IO.File]::ReadAllBytes($verifier)
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }
    if ($verifierBytes.LongLength -ne [int64]$sizeValue -or
        (Get-ReleaseGateSha256Hex -Bytes $verifierBytes) -cne [string]$shaValue) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }
    try {
        [void]$strictUtf8.GetString($verifierBytes)
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
    }

    return [pscustomobject]@{
        Bytes = $verifierBytes
        Sha256 = [string]$shaValue
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
    if ([string]::Equals($Candidate, $Container, $comparison)) { return $true }
    return $Candidate.StartsWith(
        $Container + [IO.Path]::DirectorySeparatorChar,
        $comparison
    )
}

function Resolve-StagingVerifierTempDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$ManifestPath
    )

    $candidate = if ($script:IsWindowsPlatform) {
        $localApplicationData = [Environment]::GetFolderPath(
            [Environment+SpecialFolder]::LocalApplicationData
        )
        if ([string]::IsNullOrWhiteSpace($localApplicationData)) {
            Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_TEMP_INVALID'
        }
        Join-Path $localApplicationData 'Temp'
    } elseif ($script:IsMacOSPlatform) {
        '/private/tmp'
    } else {
        '/tmp'
    }
    if (-not (Test-Path -LiteralPath $candidate -PathType Container)) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_TEMP_INVALID'
    }
    try {
        $tempFull = (Resolve-Path -LiteralPath $candidate).Path
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_TEMP_INVALID'
    }
    Assert-NoReparseAncestry `
        -Path $tempFull `
        -Code 'RG_STAGING_VERIFIER_TEMP_INVALID'

    $artifactDirectory = [IO.Path]::GetDirectoryName($ManifestPath)
    if ((Test-PathInside -Candidate $tempFull -Container $Root) -or
        (Test-PathInside -Candidate $tempFull -Container $artifactDirectory)) {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_TEMP_INVALID'
    }
    return $tempFull
}

function Assert-ArtifactInitialLayout {
    param(
        [Parameter(Mandatory = $true)][string]$Artifact,
        [Parameter(Mandatory = $true)][string]$ManifestPath
    )
    try {
        $entries = @(Get-ChildItem -LiteralPath $Artifact -Force)
    } catch {
        Fail-ReleaseGate -Code 'RG_ARTIFACT_INITIAL_LAYOUT'
    }
    if ($entries.Count -ne 1 -or
        -not ($entries[0] -is [IO.FileInfo]) -or
        ($entries[0].Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        -not [string]::Equals(
            $entries[0].FullName,
            $ManifestPath,
            [StringComparison]::OrdinalIgnoreCase
        )) {
        Fail-ReleaseGate -Code 'RG_ARTIFACT_INITIAL_LAYOUT'
    }
}

function Assert-FinalBuildArtifacts {
    param(
        [Parameter(Mandatory = $true)][string]$Bin,
        [Parameter(Mandatory = $true)][object[]]$Targets
    )

    try {
        $binItem = Get-Item -LiteralPath $Bin -Force
        $entries = @(Get-ChildItem -LiteralPath $Bin -Force)
    } catch {
        Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
    }
    $binLinkType = $binItem.PSObject.Properties['LinkType']
    if (-not ($binItem -is [IO.DirectoryInfo]) -or
        ($binItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        ($null -ne $binLinkType -and
            -not [string]::IsNullOrWhiteSpace([string]$binLinkType.Value))) {
        Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
    }
    Assert-NoReparseAncestry -Path $Bin -Code 'RG_BUILD_ARTIFACT_INVALID'

    $expectedNames = @($Targets | ForEach-Object { [string]$_.File })
    if ($entries.Count -ne $expectedNames.Count) {
        Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
    }

    $streamParameterAvailable = $false
    if ($script:IsWindowsPlatform) {
        try {
            $getItemCommand = Get-Command Get-Item -CommandType Cmdlet -ErrorAction Stop
            $streamParameterAvailable = $getItemCommand.Parameters.ContainsKey('Stream')
        } catch {
            $streamParameterAvailable = $false
        }
    }

    foreach ($expectedName in $expectedNames) {
        $matches = @($entries | Where-Object { $_.Name -ceq $expectedName })
        if ($matches.Count -ne 1) {
            Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
        }
        $item = $matches[0]
        $linkType = $item.PSObject.Properties['LinkType']
        if (-not ($item -is [IO.FileInfo]) -or
            ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            ($null -ne $linkType -and
                -not [string]::IsNullOrWhiteSpace([string]$linkType.Value)) -or
            $item.Length -le 0) {
            Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
        }
        Assert-NoReparseAncestry -Path $item.FullName -Code 'RG_BUILD_ARTIFACT_INVALID'

        if ($script:IsWindowsPlatform) {
            if (($item.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) {
                Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
            }
            if ($streamParameterAvailable) {
                try {
                    $streams = @(Get-Item -LiteralPath $item.FullName -Stream * -ErrorAction Stop)
                } catch {
                    Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
                }
                if (@($streams | Where-Object { $_.Stream -cne ':$DATA' }).Count -ne 0) {
                    Fail-ReleaseGate -Code 'RG_BUILD_ARTIFACT_INVALID'
                }
            }
        }
    }
}

function Initialize-ArtifactLayout {
    param([Parameter(Mandatory = $true)][string]$Artifact)

    $paths = [ordered]@{
        Bin = Join-Path $Artifact 'bin'
        Evidence = Join-Path $Artifact 'evidence/local'
        CacheBuild = Join-Path $Artifact 'cache/build'
        CacheModule = Join-Path $Artifact 'cache/module'
        CacheGoTmp = Join-Path $Artifact 'cache/go-tmp'
        CacheGoPath = Join-Path $Artifact 'cache/gopath'
        Tmp = Join-Path $Artifact 'tmp'
    }
    try {
        foreach ($path in $paths.Values) {
            [void][IO.Directory]::CreateDirectory($path)
        }
    } catch {
        Fail-ReleaseGate -Code 'RG_ARTIFACT_INITIALIZATION_FAILED'
    }
    return [pscustomobject]$paths
}

function Set-IsolatedGoEnvironment {
    param([Parameter(Mandatory = $true)]$Paths)

    Set-ProcessEnvironment -Name 'CGO_ENABLED' -Value $null
    Set-ProcessEnvironment -Name 'GOOS' -Value $null
    Set-ProcessEnvironment -Name 'GOARCH' -Value $null
    Set-ProcessEnvironment -Name 'CC' -Value $null
    Set-ProcessEnvironment -Name 'GOCACHE' -Value $Paths.CacheBuild
    Set-ProcessEnvironment -Name 'GOMODCACHE' -Value $Paths.CacheModule
    Set-ProcessEnvironment -Name 'GOTMPDIR' -Value $Paths.CacheGoTmp
    Set-ProcessEnvironment -Name 'TEMP' -Value $Paths.Tmp
    Set-ProcessEnvironment -Name 'TMP' -Value $Paths.Tmp
    Set-ProcessEnvironment -Name 'TMPDIR' -Value $Paths.Tmp
    Set-ProcessEnvironment -Name 'GOWORK' -Value 'off'
    Set-ProcessEnvironment -Name 'GOFLAGS' -Value '-mod=readonly'
    Set-ProcessEnvironment -Name 'GOENV' -Value 'off'
    Set-ProcessEnvironment -Name 'GOTOOLCHAIN' -Value 'local'
    Set-ProcessEnvironment -Name 'GOPATH' -Value $Paths.CacheGoPath
}

function Resolve-GofmtCommand {
    param([string]$GoCommand)

    $toolDirectory = Split-Path -Parent $GoCommand
    foreach ($name in @('gofmt.exe', 'gofmt.cmd', 'gofmt')) {
        $candidate = Join-Path $toolDirectory $name
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $full = (Resolve-Path -LiteralPath $candidate).Path
            Assert-NoReparseAncestry -Path $full -Code 'RG_GOFMT_REPARSE'
            return $full
        }
    }

    Fail-ReleaseGate -Code 'RG_GOFMT_MISSING'
}

function Invoke-NativeChecked {
    param(
        [string]$Command,
        [string[]]$Arguments
    )

    Write-Host ('> ' + $Command + ' ' + ($Arguments -join ' '))
    $savedErrorPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $Command @Arguments
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorPreference
    }
    if ($exitCode -ne 0) {
        throw "RELEASE_GATE_FAIL code=RG_NATIVE_COMMAND_FAILED exit=$exitCode command=$Command"
    }
}

function Invoke-StagingVerification {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ManifestPin,
        [Parameter(Mandatory = $true)][ValidateSet('pre', 'post')][string]$Phase
    )

    $authenticated = Get-AuthenticatedStagingVerifier `
        -Root $Root `
        -ManifestPath $ManifestPath `
        -ManifestPin $ManifestPin
    $powerShellCommand = (Get-Process -Id $PID).Path
    if (-not [IO.Path]::IsPathRooted($powerShellCommand) -or
        -not (Test-Path -LiteralPath $powerShellCommand -PathType Leaf)) {
        Fail-ReleaseGate -Code 'RG_POWERSHELL_COMMAND_INVALID'
    }

    $utf8 = New-Object Text.UTF8Encoding($false)
    $verifierTemp = Resolve-StagingVerifierTempDirectory `
        -Root $Root `
        -ManifestPath $ManifestPath
    $verifierBase64 = [Convert]::ToBase64String($authenticated.Bytes)
    $rootBase64 = [Convert]::ToBase64String($utf8.GetBytes($Root))
    $manifestBase64 = [Convert]::ToBase64String($utf8.GetBytes($ManifestPath))
    $pinBase64 = [Convert]::ToBase64String($utf8.GetBytes($ManifestPin))
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
    [byte[]]$bootstrapBytes = $utf8.GetBytes($bootstrap)
    $transportBootstrap = @(
        '$ProgressPreference = ''SilentlyContinue''',
        '$inputStream = [Console]::OpenStandardInput()',
        '$memory = New-Object IO.MemoryStream',
        'try {',
        '    $inputStream.CopyTo($memory)',
        '    [byte[]]$bytes = $memory.ToArray()',
        '} finally {',
        '    $memory.Dispose()',
        '}',
        '$offset = 0',
        'if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {',
        '    $offset = 3',
        '} elseif ($bytes.Length -ge 2 -and (($bytes[0] -eq 0xFF -and $bytes[1] -eq 0xFE) -or ($bytes[0] -eq 0xFE -and $bytes[1] -eq 0xFF))) {',
        '    $offset = 2',
        '}',
        '$strictUtf8 = New-Object Text.UTF8Encoding($false, $true)',
        '$commandText = $strictUtf8.GetString($bytes, $offset, $bytes.Length - $offset)',
        '& ([ScriptBlock]::Create($commandText))',
        'if ($?) { exit 0 }',
        'exit 1'
    ) -join "`n"
    $transportBase64 = [Convert]::ToBase64String(
        [Text.Encoding]::Unicode.GetBytes($transportBootstrap)
    )

    Write-Host "> staging verification: $Phase"
    $process = $null
    try {
        $startInfo = New-Object Diagnostics.ProcessStartInfo
        $startInfo.FileName = $powerShellCommand
        $startInfo.Arguments =
            '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass ' +
            '-EncodedCommand ' + $transportBase64
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.WorkingDirectory = $verifierTemp
        $startInfo.RedirectStandardInput = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        if ($null -ne $startInfo.PSObject.Properties['StandardInputEncoding']) {
            $startInfo.StandardInputEncoding = $utf8
        }
        foreach ($tempName in @('TEMP', 'TMP', 'TMPDIR')) {
            $startInfo.EnvironmentVariables[$tempName] = $verifierTemp
        }
        if ($null -ne $startInfo.PSObject.Properties['StandardOutputEncoding']) {
            $startInfo.StandardOutputEncoding = $utf8
            $startInfo.StandardErrorEncoding = $utf8
        }

        $process = New-Object Diagnostics.Process
        $process.StartInfo = $startInfo
        if (-not $process.Start()) {
            Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_EXECUTION_FAILED'
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.StandardInput.BaseStream.Write(
            $bootstrapBytes,
            0,
            $bootstrapBytes.Length
        )
        $process.StandardInput.BaseStream.Close()
        $process.WaitForExit()
        $exitCode = $process.ExitCode
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
    } catch {
        Fail-ReleaseGate -Code 'RG_STAGING_VERIFIER_EXECUTION_FAILED'
    } finally {
        if ($null -ne $process) {
            $process.Dispose()
        }
    }
    foreach ($text in @($stdout, $stderr)) {
        if (-not [string]::IsNullOrWhiteSpace($text)) {
            foreach ($line in ($text -split '\r?\n')) {
                if (-not [string]::IsNullOrEmpty($line)) {
                    Write-Host $line
                }
            }
        }
    }
    if ($exitCode -ne 0) {
        if ($Phase -ceq 'pre') {
            Fail-ReleaseGate -Code 'RG_PRE_VERIFY_FAILED'
        }
        Fail-ReleaseGate -Code 'RG_POST_VERIFY_FAILED'
    }
}

function Invoke-PowerShellGateSequence {
    param(
        [Parameter(Mandatory = $true)][string]$PowerShellCommand,
        [Parameter(Mandatory = $true)][object[]]$GateInvocations
    )

    if (-not [IO.Path]::IsPathRooted($PowerShellCommand) -or
        -not (Test-Path -LiteralPath $PowerShellCommand -PathType Leaf)) {
        throw "PowerShell child-process command is not an absolute executable path: $PowerShellCommand"
    }
    foreach ($invocation in $GateInvocations) {
        $name = [string]$invocation.Name
        $scriptPath = [string]$invocation.ScriptPath
        $scriptArguments = @($invocation.Arguments)
        if ([string]::IsNullOrWhiteSpace($name) -or
            -not [IO.Path]::IsPathRooted($scriptPath) -or
            -not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
            throw "Permanent gate invocation is invalid: name=$name script=$scriptPath"
        }

        $childArguments = @(
            '-NoLogo',
            '-NoProfile',
            '-NonInteractive',
            '-File',
            $scriptPath
        ) + $scriptArguments
        Write-Host "> permanent gate: $name"
        & $PowerShellCommand @childArguments
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            throw "Permanent gate $name exited with code $exitCode."
        }
    }
}

function Invoke-PermanentGates {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$GoCommand,
        [Parameter(Mandatory = $true)][string]$GofmtCommand
    )

    foreach ($absolutePath in @($Root, $GoCommand, $GofmtCommand)) {
        if (-not [IO.Path]::IsPathRooted($absolutePath)) {
            throw "Permanent gates require absolute paths: $absolutePath"
        }
    }
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        throw "Permanent gate repository root is unavailable: $Root"
    }
    foreach ($toolPath in @($GoCommand, $GofmtCommand)) {
        if (-not (Test-Path -LiteralPath $toolPath -PathType Leaf)) {
            throw "Permanent gate tool is unavailable: $toolPath"
        }
    }

    $powerShellCommand = (Get-Process -Id $PID).Path
    $gateInvocations = @(
        [pscustomobject]@{
            Name = 'PublicTree'
            ScriptPath = Join-Path $PSScriptRoot 'Test-PublicTree.ps1'
            Arguments = @('-Root', $Root)
        },
        [pscustomobject]@{
            Name = 'License'
            ScriptPath = Join-Path $PSScriptRoot 'Test-License.ps1'
            Arguments = @('-Root', $Root, '-GoCommand', $GoCommand)
        },
        [pscustomobject]@{
            Name = 'Docs'
            ScriptPath = Join-Path $PSScriptRoot 'Test-Docs.ps1'
            Arguments = @('-Root', $Root, '-GofmtPath', $GofmtCommand)
        }
    )
    Invoke-PowerShellGateSequence `
        -PowerShellCommand $powerShellCommand `
        -GateInvocations $gateInvocations
}

function Test-GoFormatting {
    param([string]$GofmtCommand)

    $excludedRoots = @('.git', '.tools', 'bin', 'data', 'logs', 'storage')
    $excludedPrefixes = @('.cache/go-build', '.cache/go-tmp')
    $goFiles = @(Get-ChildItem -LiteralPath $repositoryRoot -Recurse -File -Filter '*.go' -Force |
        Where-Object {
            $relative = $_.FullName.Substring($repositoryRoot.Length).TrimStart([char[]]@([char]92, [char]47))
            $relative = $relative.Replace([char]92, [char]47)
            $firstSegment = ($relative -split '[\\/]', 2)[0]
            $excludedByPrefix = $false
            foreach ($prefix in $excludedPrefixes) {
                if ($relative -eq $prefix -or $relative.StartsWith($prefix + '/', [StringComparison]::OrdinalIgnoreCase)) {
                    $excludedByPrefix = $true
                    break
                }
            }
            ($excludedRoots -notcontains $firstSegment) -and -not $excludedByPrefix
        } |
        Sort-Object FullName)

    if ($goFiles.Count -eq 0) {
        throw 'No Go source files were found.'
    }

    $unformatted = New-Object System.Collections.Generic.List[string]
    for ($offset = 0; $offset -lt $goFiles.Count; $offset += 25) {
        $last = [Math]::Min($offset + 24, $goFiles.Count - 1)
        $batch = @($goFiles[$offset..$last] | ForEach-Object { $_.FullName })
        $output = @(& $GofmtCommand '-l' @batch)
        if ($LASTEXITCODE -ne 0) {
            throw "gofmt exited with code $LASTEXITCODE."
        }
        foreach ($line in $output) {
            if (-not [string]::IsNullOrWhiteSpace($line)) {
                $unformatted.Add($line)
            }
        }
    }

    if ($unformatted.Count -gt 0) {
        $unformatted | ForEach-Object { Write-Error "gofmt required: $_" -ErrorAction Continue }
        throw "Formatting gate failed for $($unformatted.Count) file(s)."
    }
    Write-Host "Formatting gate passed for $($goFiles.Count) Go file(s)."
}

function Find-CCompiler {
    if (-not [string]::IsNullOrWhiteSpace($env:CC)) {
        $configured = @(
            Get-Command $env:CC -CommandType Application -ErrorAction SilentlyContinue
        ) | Select-Object -First 1
        if ($null -ne $configured) {
            return $configured.Source
        }
    }

    foreach ($name in @('gcc', 'clang', 'cc')) {
        $command = @(
            Get-Command $name -CommandType Application -ErrorAction SilentlyContinue
        ) | Select-Object -First 1
        if ($null -ne $command) {
            return $command.Source
        }
    }
    return $null
}

function Test-RaceSupported {
    param(
        [string]$OperatingSystem,
        [string]$Architecture
    )

    $supported = @(
        'darwin/amd64', 'darwin/arm64',
        'freebsd/amd64',
        'linux/amd64', 'linux/arm64', 'linux/ppc64le', 'linux/s390x',
        'windows/amd64'
    )
    return $supported -contains "$OperatingSystem/$Architecture"
}

function Assert-ReleaseEvidenceState {
    param(
        [Parameter(Mandatory = $true)][string]$Status,
        [Parameter(Mandatory = $true)][string]$CommandOutcome
    )

    $allowed = @(
        'PASS/PASSED',
        'UNPROVED/NOT_RUN',
        'UNPROVED/FAILED',
        'UNPROVED/PASSED',
        'SKIPPED/NOT_RUN'
    )
    $combination = "$Status/$CommandOutcome"
    if ($allowed -cnotcontains $combination) {
        throw "Invalid release evidence state: $combination."
    }
}

function Write-ReleaseEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Record
    )

    Assert-ReleaseEvidenceState `
        -Status ([string]$Record.status) `
        -CommandOutcome ([string]$Record.command_outcome)
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Path $parent -Force | Out-Null
    $json = $Record | ConvertTo-Json -Depth 8
    [IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function New-ReleaseEvidenceRecord {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Required,
        [Parameter(Mandatory = $true)][string]$Status,
        [Parameter(Mandatory = $true)][string]$CommandOutcome,
        [AllowNull()][Nullable[int]]$ExitCode,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Command,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$CommandArguments,
        [AllowNull()][string]$StartedAtUtc,
        [AllowNull()][string]$FinishedAtUtc,
        [Parameter(Mandatory = $true)][long]$ElapsedMilliseconds,
        [Parameter(Mandatory = $true)][string]$GoVersion,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$GoEnvironment,
        [AllowNull()][string]$CompilerPath,
        [AllowNull()][string]$CompilerIdentity,
        [AllowNull()][string]$CompilerVersion,
        [AllowNull()][string]$GoTestJSON,
        [string[]]$Packages = @(),
        [AllowNull()][string]$Reason
    )

    Assert-ReleaseEvidenceState -Status $Status -CommandOutcome $CommandOutcome
    return [ordered]@{
        schema_version = 1
        name = $Name
        required = $Required
        status = $Status
        command_outcome = $CommandOutcome
        exit_code = $ExitCode
        command = $Command
        command_argv = @($CommandArguments)
        started_at_utc = $StartedAtUtc
        finished_at_utc = $FinishedAtUtc
        elapsed_ms = $ElapsedMilliseconds
        go_version = $GoVersion
        runner = [ordered]@{
            kind = 'local'
            name = [Environment]::MachineName
            os = [Environment]::OSVersion.Platform.ToString()
            process_architecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()
        }
        go_env = $GoEnvironment
        compiler_path = $CompilerPath
        compiler_identity = $CompilerIdentity
        compiler_version = $CompilerVersion
        go_test_json = $GoTestJSON
        packages = @($Packages)
        reason = $Reason
    }
}

function Invoke-GoTestWithEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$GoCommand,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string[]]$Packages,
        [Parameter(Mandatory = $true)][string]$EvidenceRoot,
        [Parameter(Mandatory = $true)][string]$GoVersion,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$GoEnvironment,
        [AllowNull()][string]$CompilerPath,
        [AllowNull()][string]$CompilerIdentity,
        [AllowNull()][string]$CompilerVersion,
        [bool]$ProofComplete = $true
    )

    $jsonPath = Join-Path $EvidenceRoot "$Name.go-test.json"
    $manifestPath = Join-Path $EvidenceRoot "$Name.evidence.json"
    New-Item -ItemType Directory -Path $EvidenceRoot -Force | Out-Null
    if (Test-Path -LiteralPath $jsonPath -PathType Leaf) {
        Remove-Item -LiteralPath $jsonPath -Force
    }

    $commandArguments = @($Arguments) + @($Packages)
    $commandText = $GoCommand + ' ' + ($commandArguments -join ' ')
    Write-Host "> $commandText"
    $started = [DateTimeOffset]::UtcNow
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    & $GoCommand @commandArguments 2>&1 |
        Tee-Object -FilePath $jsonPath |
        ForEach-Object { Write-Host $_ }
    $nativeExitCode = $LASTEXITCODE
    $stopwatch.Stop()
    $finished = [DateTimeOffset]::UtcNow

    if ($nativeExitCode -eq 0) {
        $commandOutcome = 'PASSED'
        if ($ProofComplete) {
            $status = 'PASS'
        } else {
            $status = 'UNPROVED'
        }
    } else {
        $commandOutcome = 'FAILED'
        $status = 'UNPROVED'
    }

    $record = New-ReleaseEvidenceRecord `
        -Name $Name `
        -Required $true `
        -Status $status `
        -CommandOutcome $commandOutcome `
        -ExitCode $nativeExitCode `
        -Command $commandText `
        -CommandArguments (@($GoCommand) + $commandArguments) `
        -StartedAtUtc $started.ToString('O') `
        -FinishedAtUtc $finished.ToString('O') `
        -ElapsedMilliseconds $stopwatch.ElapsedMilliseconds `
        -GoVersion $GoVersion `
        -GoEnvironment $GoEnvironment `
        -CompilerPath $CompilerPath `
        -CompilerIdentity $CompilerIdentity `
        -CompilerVersion $CompilerVersion `
        -GoTestJSON ([IO.Path]::GetFileName($jsonPath)) `
        -Packages $Packages `
        -Reason $(if ($ProofComplete) { $null } else { 'command ran, but required environment proof is incomplete' })
    Write-ReleaseEvidence -Path $manifestPath -Record $record
    return [pscustomobject]@{
        ExitCode = $nativeExitCode
        ManifestPath = $manifestPath
        JSONPath = $jsonPath
    }
}

function Write-NotRunRaceEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$EvidenceRoot,
        [Parameter(Mandatory = $true)][string]$Status,
        [Parameter(Mandatory = $true)][string]$Reason,
        [Parameter(Mandatory = $true)][string]$GoVersion,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$GoEnvironment,
        [AllowNull()][string]$CompilerPath,
        [AllowNull()][string]$CompilerIdentity,
        [AllowNull()][string]$CompilerVersion
    )

    $record = New-ReleaseEvidenceRecord `
        -Name $Name `
        -Required ($Status -cne 'SKIPPED') `
        -Status $Status `
        -CommandOutcome 'NOT_RUN' `
        -ExitCode $null `
        -Command '' `
        -CommandArguments @() `
        -StartedAtUtc $null `
        -FinishedAtUtc $null `
        -ElapsedMilliseconds 0 `
        -GoVersion $GoVersion `
        -GoEnvironment $GoEnvironment `
        -CompilerPath $CompilerPath `
        -CompilerIdentity $CompilerIdentity `
        -CompilerVersion $CompilerVersion `
        -GoTestJSON $null `
        -Packages @() `
        -Reason $Reason
    Write-ReleaseEvidence -Path (Join-Path $EvidenceRoot "$Name.evidence.json") -Record $record
}

$targets = @(
    @{ OS = 'windows'; Arch = 'amd64'; File = 'freeagent-windows-amd64.exe' },
    @{ OS = 'windows'; Arch = 'arm64'; File = 'freeagent-windows-arm64.exe' },
    @{ OS = 'linux'; Arch = 'amd64'; File = 'freeagent-linux-amd64' },
    @{ OS = 'linux'; Arch = 'arm64'; File = 'freeagent-linux-arm64' },
    @{ OS = 'darwin'; Arch = 'amd64'; File = 'freeagent-darwin-amd64' },
    @{ OS = 'darwin'; Arch = 'arm64'; File = 'freeagent-darwin-arm64' }
)

$nativeErrorPreferenceVariable = Get-Variable `
    -Name 'PSNativeCommandUseErrorActionPreference' `
    -ErrorAction SilentlyContinue
$hasNativeErrorPreference = $null -ne $nativeErrorPreferenceVariable
$savedNativeErrorPreference = if ($hasNativeErrorPreference) {
    [bool]$nativeErrorPreferenceVariable.Value
} else {
    $null
}

$preVerificationPassed = $false
$primaryError = $null
$postError = $null
$restoreFailures = New-Object 'System.Collections.Generic.List[string]'
$artifactFull = $null
$manifestFull = $null
$manifestPin = $null
$artifactPaths = @()

try {
    try {
        if ($ManifestSha256 -cnotmatch '^[0-9A-Fa-f]{64}$') {
            Fail-ReleaseGate -Code 'RG_MANIFEST_PIN_INVALID'
        }
        $manifestPin = $ManifestSha256.ToLowerInvariant()
        $artifactFull = Get-NormalizedAbsolutePath -Value $ArtifactRoot
        $go = Get-NormalizedAbsolutePath -Value $GoCommand

        if (-not (Test-Path -LiteralPath $artifactFull)) {
            Fail-ReleaseGate -Code 'RG_ARTIFACT_MISSING'
        }
        if (-not (Test-Path -LiteralPath $artifactFull -PathType Container)) {
            Fail-ReleaseGate -Code 'RG_ARTIFACT_NOT_DIRECTORY'
        }
        Assert-NoReparseAncestry -Path $artifactFull -Code 'RG_ARTIFACT_REPARSE'

        if (-not (Test-Path -LiteralPath $go -PathType Leaf)) {
            Fail-ReleaseGate -Code 'RG_GO_COMMAND_MISSING'
        }
        Assert-NoReparseAncestry -Path $go -Code 'RG_GO_COMMAND_REPARSE'
        $go = (Resolve-Path -LiteralPath $go).Path

        if ((Test-PathInside -Candidate $artifactFull -Container $repositoryRoot) -or
            (Test-PathInside -Candidate $repositoryRoot -Container $artifactFull)) {
            Fail-ReleaseGate -Code 'RG_ROOTS_NOT_DISJOINT'
        }
        if ((Test-PathInside -Candidate $go -Container $repositoryRoot) -or
            (Test-PathInside -Candidate $go -Container $artifactFull)) {
            Fail-ReleaseGate -Code 'RG_GO_COMMAND_DISALLOWED'
        }

        $manifestFull = Join-Path $artifactFull $script:ManifestName
        Assert-ArtifactInitialLayout -Artifact $artifactFull -ManifestPath $manifestFull
        Assert-NoReparseAncestry -Path $manifestFull -Code 'RG_ARTIFACT_REPARSE'

        if ($hasNativeErrorPreference) {
            Set-Variable `
                -Name 'PSNativeCommandUseErrorActionPreference' `
                -Value $false `
                -Scope Script
        }

        Invoke-StagingVerification `
            -Root $repositoryRoot `
            -ManifestPath $manifestFull `
            -ManifestPin $manifestPin `
            -Phase 'pre'
        $preVerificationPassed = $true

        $artifactLayout = Initialize-ArtifactLayout -Artifact $artifactFull
        Set-IsolatedGoEnvironment -Paths $artifactLayout
        Set-Location -LiteralPath $repositoryRoot

        $gofmt = Resolve-GofmtCommand -GoCommand $go
        if ((Test-PathInside -Candidate $gofmt -Container $repositoryRoot) -or
            (Test-PathInside -Candidate $gofmt -Container $artifactFull)) {
            Fail-ReleaseGate -Code 'RG_GOFMT_DISALLOWED'
        }

        Invoke-NativeChecked -Command $go -Arguments @('mod', 'download', 'all')
        Invoke-PermanentGates `
            -Root $repositoryRoot `
            -GoCommand $go `
            -GofmtCommand $gofmt

        $goVersionLines = @(& $go version 2>&1)
        $goVersionExit = $LASTEXITCODE
        if ($goVersionExit -ne 0) {
            throw "RELEASE_GATE_FAIL code=RG_GO_VERSION_FAILED exit=$goVersionExit"
        }
        $goVersion = ($goVersionLines -join [Environment]::NewLine).Trim()
        Write-Host $goVersion

        $powerShellCommand = (Get-Process -Id $PID).Path
        Invoke-PowerShellGateSequence `
            -PowerShellCommand $powerShellCommand `
            -GateInvocations @(
                [pscustomobject]@{
                    Name = 'Branding'
                    ScriptPath = Join-Path $PSScriptRoot 'Test-Branding.ps1'
                    Arguments = @('-RepositoryRoot', $repositoryRoot)
                }
            )
        Test-GoFormatting -GofmtCommand $gofmt
        Invoke-NativeChecked -Command $go -Arguments @('mod', 'verify')
        Invoke-NativeChecked -Command $go -Arguments @('vet', './...')

        $hostGoEnvironmentLines = @(& $go env GOOS GOARCH CGO_ENABLED)
        $hostGoEnvironmentExit = $LASTEXITCODE
        if ($hostGoEnvironmentExit -ne 0 -or $hostGoEnvironmentLines.Count -ne 3) {
            Fail-ReleaseGate -Code 'RG_GO_ENV_FAILED'
        }
        $hostGoEnvironment = [ordered]@{
            GOOS = ([string]$hostGoEnvironmentLines[0]).Trim()
            GOARCH = ([string]$hostGoEnvironmentLines[1]).Trim()
            CGO_ENABLED = ([string]$hostGoEnvironmentLines[2]).Trim()
        }
        $hostGoos = $hostGoEnvironment.GOOS
        $hostGoarch = $hostGoEnvironment.GOARCH
        $evidenceDirectory = $artifactLayout.Evidence

        $ordinaryEvidence = Invoke-GoTestWithEvidence `
            -GoCommand $go `
            -Name 'ordinary' `
            -Arguments @('test', '-json', '-shuffle=on', '-count=1', '-timeout=30m') `
            -Packages @('./...') `
            -EvidenceRoot $evidenceDirectory `
            -GoVersion $goVersion `
            -GoEnvironment $hostGoEnvironment
        if ($ordinaryEvidence.ExitCode -ne 0) {
            throw (
                'RELEASE_GATE_FAIL code=RG_ORDINARY_TEST_FAILED ' +
                "exit=$($ordinaryEvidence.ExitCode) evidence=$($ordinaryEvidence.ManifestPath)"
            )
        }

        $compiler = Find-CCompiler
        if ($SkipRace) {
            Write-NotRunRaceEvidence `
                -Name 'race' `
                -EvidenceRoot $evidenceDirectory `
                -Status 'UNPROVED' `
                -Reason 'race detector explicitly skipped by -SkipRace' `
                -GoVersion $goVersion `
                -GoEnvironment $hostGoEnvironment `
                -CompilerPath $compiler
            Write-Warning 'Race detector was explicitly skipped.'
        } elseif (-not (Test-RaceSupported -OperatingSystem $hostGoos -Architecture $hostGoarch)) {
            Write-NotRunRaceEvidence `
                -Name 'race' `
                -EvidenceRoot $evidenceDirectory `
                -Status 'SKIPPED' `
                -Reason "Go race is inapplicable on $hostGoos/$hostGoarch" `
                -GoVersion $goVersion `
                -GoEnvironment $hostGoEnvironment `
                -CompilerPath $compiler
            Write-Warning "Race detector skipped because $hostGoos/$hostGoarch is not supported by Go."
        } elseif ($null -eq $compiler) {
            Write-NotRunRaceEvidence `
                -Name 'race' `
                -EvidenceRoot $evidenceDirectory `
                -Status 'UNPROVED' `
                -Reason 'required C compiler was not found' `
                -GoVersion $goVersion `
                -GoEnvironment $hostGoEnvironment
            Fail-ReleaseGate -Code 'RG_RACE_COMPILER_MISSING'
        } else {
            Write-Host "Using C compiler for race gate: $compiler"
            $compilerVersionLines = @(& $compiler '--version' 2>&1)
            $compilerVersionExitCode = $LASTEXITCODE
            $compilerVersion = ($compilerVersionLines -join [Environment]::NewLine).Trim()
            $compilerIdentity = if ($compilerVersionLines.Count -gt 0) {
                ([string]$compilerVersionLines[0]).Trim()
            } else {
                [IO.Path]::GetFileName($compiler)
            }
            $compilerIsGCC = (
                [IO.Path]::GetFileNameWithoutExtension($compiler).StartsWith(
                    'gcc',
                    [StringComparison]::OrdinalIgnoreCase
                ) -or
                $compilerVersion.IndexOf('gcc', [StringComparison]::OrdinalIgnoreCase) -ge 0 -or
                $compilerVersion.IndexOf('Free Software Foundation', [StringComparison]::OrdinalIgnoreCase) -ge 0
            )
            $compilerIdentityProofComplete = (
                $hostGoos -ceq 'linux' -and
                $compilerVersionExitCode -eq 0 -and
                -not [string]::IsNullOrWhiteSpace($compilerVersion) -and
                $compilerIsGCC
            )

            Set-ProcessEnvironment -Name 'CGO_ENABLED' -Value '1'
            Set-ProcessEnvironment -Name 'CC' -Value $compiler
            Set-ProcessEnvironment -Name 'GOOS' -Value $null
            Set-ProcessEnvironment -Name 'GOARCH' -Value $null
            $raceGoEnvironmentLines = @(& $go env GOOS GOARCH CGO_ENABLED)
            $raceGoEnvironmentExit = $LASTEXITCODE
            if ($raceGoEnvironmentExit -ne 0 -or $raceGoEnvironmentLines.Count -ne 3) {
                Fail-ReleaseGate -Code 'RG_RACE_GO_ENV_FAILED'
            }
            $raceGoEnvironment = [ordered]@{
                GOOS = ([string]$raceGoEnvironmentLines[0]).Trim()
                GOARCH = ([string]$raceGoEnvironmentLines[1]).Trim()
                CGO_ENABLED = ([string]$raceGoEnvironmentLines[2]).Trim()
            }
            $raceProofComplete = (
                $compilerIdentityProofComplete -and
                $raceGoEnvironment.GOOS -ceq 'linux' -and
                $raceGoEnvironment.CGO_ENABLED -ceq '1'
            )

            Write-NotRunRaceEvidence `
                -Name 'race' `
                -EvidenceRoot $evidenceDirectory `
                -Status 'UNPROVED' `
                -Reason 'race test has not started' `
                -GoVersion $goVersion `
                -GoEnvironment $raceGoEnvironment `
                -CompilerPath $compiler `
                -CompilerIdentity $compilerIdentity `
                -CompilerVersion $compilerVersion

            $raceEvidence = Invoke-GoTestWithEvidence `
                -GoCommand $go `
                -Name 'race' `
                -Arguments @('test', '-json', '-race', '-count=1', "-timeout=$RaceTestTimeout") `
                -Packages @('./...') `
                -EvidenceRoot $evidenceDirectory `
                -GoVersion $goVersion `
                -GoEnvironment $raceGoEnvironment `
                -CompilerPath $compiler `
                -CompilerIdentity $compilerIdentity `
                -CompilerVersion $compilerVersion `
                -ProofComplete $raceProofComplete
            if ($raceEvidence.ExitCode -ne 0) {
                Fail-ReleaseGate -Code 'RG_RACE_TEST_FAILED'
            }
        }

        Set-ProcessEnvironment -Name 'CGO_ENABLED' -Value '0'
        Set-ProcessEnvironment -Name 'CC' -Value $null
        $artifactPaths = @()
        foreach ($target in $targets) {
            Set-ProcessEnvironment -Name 'GOOS' -Value $target.OS
            Set-ProcessEnvironment -Name 'GOARCH' -Value $target.Arch
            $output = Join-Path $artifactLayout.Bin $target.File
            Invoke-NativeChecked `
                -Command $go `
                -Arguments @('build', '-trimpath', '-o', $output, './cmd/freeagent')
            $artifactPaths += $output
        }
        Assert-FinalBuildArtifacts -Bin $artifactLayout.Bin -Targets $targets
    } catch {
        $primaryError = $_
    }
} finally {
    if ($preVerificationPassed) {
        try {
            Invoke-StagingVerification `
                -Root $repositoryRoot `
                -ManifestPath $manifestFull `
                -ManifestPin $manifestPin `
                -Phase 'post'
        } catch {
            $postError = $_
        }
    }

    foreach ($environmentName in $script:EnvironmentNames) {
        try {
            Set-ProcessEnvironment `
                -Name $environmentName `
                -Value $savedEnvironment[$environmentName]
        } catch {
            $restoreFailures.Add("environment:$environmentName`:$($_.Exception.Message)")
        }
    }
    try {
        Set-Location -LiteralPath $originalLocation
    } catch {
        $restoreFailures.Add("location:$($_.Exception.Message)")
    }
    if ($hasNativeErrorPreference) {
        try {
            Set-Variable `
                -Name 'PSNativeCommandUseErrorActionPreference' `
                -Value $savedNativeErrorPreference `
                -Scope Script
        } catch {
            $restoreFailures.Add("native-preference:$($_.Exception.Message)")
        }
    }
}

$failureMessage = Resolve-ReleaseGateFailure `
    -PrimaryFailed ($null -ne $primaryError) `
    -PrimaryMessage $(if ($null -ne $primaryError) { $primaryError.Exception.Message } else { $null }) `
    -PostFailed ($null -ne $postError) `
    -PostMessage $(if ($null -ne $postError) { $postError.Exception.Message } else { $null }) `
    -RestoreFailures @($restoreFailures)
if ($null -ne $failureMessage) {
    throw $failureMessage
}

Write-Host 'RELEASE_GATE_PASS'
Write-Host 'Cross-platform artifacts:'
$artifactPaths | ForEach-Object { Write-Host "  $_" }
