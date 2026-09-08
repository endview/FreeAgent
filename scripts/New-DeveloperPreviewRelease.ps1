#requires -Version 7.2

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$RepositoryRoot,
    [Parameter(Mandatory = $true)][string]$Revision,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [Parameter(Mandatory = $true)][string]$GoCommand,
    [string]$ControlPath = '',
    [string]$ArchiveToolPath = '',
    [string]$ModuleCacheSeed = '',
    [switch]$BuildOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = [Text.UTF8Encoding]::new($false)
$script:Utf8Strict = [Text.UTF8Encoding]::new($false, $true)
$script:IsWindowsPlatform =
    [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$script:Targets = @(
    @{ Goos = 'windows'; Goarch = 'amd64'; Target = 'windows-amd64' },
    @{ Goos = 'windows'; Goarch = 'arm64'; Target = 'windows-arm64' },
    @{ Goos = 'linux';   Goarch = 'amd64'; Target = 'linux-amd64' },
    @{ Goos = 'linux';   Goarch = 'arm64'; Target = 'linux-arm64' },
    @{ Goos = 'darwin';  Goarch = 'amd64'; Target = 'darwin-amd64' },
    @{ Goos = 'darwin';  Goarch = 'arm64'; Target = 'darwin-arm64' }
)

function Fail-DeveloperPreviewRelease {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "DEVELOPER_PREVIEW_RELEASE_FAIL code=$Code"
}

function Get-DPRNormalizedAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value) -or
        -not [IO.Path]::IsPathRooted($Value)) {
        Fail-DeveloperPreviewRelease -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-DeveloperPreviewRelease -Code $Code
    }
    $root = [IO.Path]::GetPathRoot($full)
    if ([string]::IsNullOrWhiteSpace($root) -or
        [string]::Equals(
            $full.TrimEnd([IO.Path]::DirectorySeparatorChar),
            $root.TrimEnd([IO.Path]::DirectorySeparatorChar),
            $script:PathComparison
        )) {
        Fail-DeveloperPreviewRelease -Code $Code
    }
    return $full.TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
}

function Assert-DPRNoReparseAncestry {
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
                Fail-DeveloperPreviewRelease -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-DeveloperPreviewRelease -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -ceq $cursor) {
            break
        }
        $cursor = $parent
    }
}

function Test-DPRPathContains {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate
    )
    if ([string]::Equals($Parent, $Candidate, $script:PathComparison)) {
        return $true
    }
    return $Candidate.StartsWith(
        $Parent + [IO.Path]::DirectorySeparatorChar,
        $script:PathComparison
    )
}

function Assert-DPRDisjointPaths {
    param(
        [Parameter(Mandatory = $true)][string[]]$Paths,
        [Parameter(Mandatory = $true)][string]$Code
    )
    for ($left = 0; $left -lt $Paths.Count; $left++) {
        for ($right = $left + 1; $right -lt $Paths.Count; $right++) {
            if ((Test-DPRPathContains -Parent $Paths[$left] -Candidate $Paths[$right]) -or
                (Test-DPRPathContains -Parent $Paths[$right] -Candidate $Paths[$left])) {
                Fail-DeveloperPreviewRelease -Code $Code
            }
        }
    }
}

function Read-DPRKeyValueFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-DPRNoReparseAncestry -Path $Path -Code $Code
    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
        $text = $script:Utf8Strict.GetString($bytes)
    } catch {
        Fail-DeveloperPreviewRelease -Code $Code
    }
    $values = [ordered]@{}
    foreach ($line in $text -split "\r?\n") {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $separator = $line.IndexOf('=')
        if ($separator -le 0) {
            Fail-DeveloperPreviewRelease -Code $Code
        }
        $key = $line.Substring(0, $separator)
        $value = $line.Substring($separator + 1)
        if ([string]::IsNullOrWhiteSpace($key) -or $values.Contains($key)) {
            Fail-DeveloperPreviewRelease -Code $Code
        }
        $values[$key] = $value
    }
    return $values
}

function Write-DPRSummary {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    $json = $Value | ConvertTo-Json -Depth 12
    [IO.File]::WriteAllText(
        $Path,
        $json + [Environment]::NewLine,
        $script:Utf8NoBom
    )
}

$repository = Get-DPRNormalizedAbsolutePath `
    -Value $RepositoryRoot `
    -Code 'DPR_REPOSITORY_INVALID'
$output = Get-DPRNormalizedAbsolutePath `
    -Value $OutputRoot `
    -Code 'DPR_OUTPUT_INVALID'
Assert-DPRNoReparseAncestry -Path $repository -Code 'DPR_REPOSITORY_REPARSE'
Assert-DPRNoReparseAncestry -Path $output -Code 'DPR_OUTPUT_REPARSE'
Assert-DPRDisjointPaths -Paths @($repository, $output) -Code 'DPR_ROOTS_OVERLAP'

if ($Revision -cnotmatch '^[0-9a-f]{40}$') {
    Fail-DeveloperPreviewRelease -Code 'DPR_REVISION_INVALID'
}
if ($Version -cnotmatch
        '^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-dev\.[1-9][0-9]*$') {
    Fail-DeveloperPreviewRelease -Code 'DPR_VERSION_INVALID'
}

$go = Get-DPRNormalizedAbsolutePath -Value $GoCommand -Code 'DPR_GO_COMMAND_INVALID'
Assert-DPRNoReparseAncestry -Path $go -Code 'DPR_GO_COMMAND_REPARSE'
if (-not (Test-Path -LiteralPath $go -PathType Leaf)) {
    Fail-DeveloperPreviewRelease -Code 'DPR_GO_COMMAND_MISSING'
}

$scriptDirectory = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($ControlPath)) {
    $ControlPath = Join-Path $scriptDirectory 'Invoke-CIWorkflowControl.ps1'
}
if ([string]::IsNullOrWhiteSpace($ArchiveToolPath)) {
    $ArchiveToolPath = Join-Path $scriptDirectory 'New-DeveloperPreviewArchives.ps1'
}
$control = Get-DPRNormalizedAbsolutePath `
    -Value $ControlPath `
    -Code 'DPR_CONTROL_PATH_INVALID'
$archiveTool = Get-DPRNormalizedAbsolutePath `
    -Value $ArchiveToolPath `
    -Code 'DPR_ARCHIVE_TOOL_PATH_INVALID'
Assert-DPRNoReparseAncestry -Path $control -Code 'DPR_CONTROL_PATH_REPARSE'
Assert-DPRNoReparseAncestry -Path $archiveTool -Code 'DPR_ARCHIVE_TOOL_PATH_REPARSE'
if (-not (Test-Path -LiteralPath $control -PathType Leaf)) {
    Fail-DeveloperPreviewRelease -Code 'DPR_CONTROL_PATH_MISSING'
}
if (-not (Test-Path -LiteralPath $archiveTool -PathType Leaf)) {
    Fail-DeveloperPreviewRelease -Code 'DPR_ARCHIVE_TOOL_PATH_MISSING'
}

$moduleCacheSeedPath = ''
if (-not [string]::IsNullOrWhiteSpace($ModuleCacheSeed)) {
    $moduleCacheSeedPath = Get-DPRNormalizedAbsolutePath `
        -Value $ModuleCacheSeed `
        -Code 'DPR_MODULE_CACHE_SEED_INVALID'
    Assert-DPRNoReparseAncestry `
        -Path $moduleCacheSeedPath `
        -Code 'DPR_MODULE_CACHE_SEED_REPARSE'
    if (-not (Test-Path -LiteralPath $moduleCacheSeedPath -PathType Container)) {
        Fail-DeveloperPreviewRelease -Code 'DPR_MODULE_CACHE_SEED_MISSING'
    }
    Assert-DPRDisjointPaths `
        -Paths @($repository, $output, $moduleCacheSeedPath) `
        -Code 'DPR_MODULE_CACHE_SEED_OVERLAP'
}

$gitStatus = @(& git -C $repository status --porcelain 2>$null)
if ($LASTEXITCODE -ne 0) {
    Fail-DeveloperPreviewRelease -Code 'DPR_GIT_STATUS_FAILED'
}
if ($gitStatus.Count -ne 0) {
    Fail-DeveloperPreviewRelease -Code 'DPR_REPOSITORY_DIRTY'
}
$headOutput = @(& git -C $repository rev-parse HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or $headOutput.Count -ne 1) {
    Fail-DeveloperPreviewRelease -Code 'DPR_REVISION_READ_FAILED'
}
$head = ([string]$headOutput[0]).Trim()
if ($head -cne $Revision) {
    Fail-DeveloperPreviewRelease -Code 'DPR_REVISION_MISMATCH'
}

if (Test-Path -LiteralPath $output) {
    Fail-DeveloperPreviewRelease -Code 'DPR_OUTPUT_EXISTS'
}
$outputParent = [IO.Path]::GetDirectoryName($output)
if ([string]::IsNullOrWhiteSpace($outputParent) -or
    -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    Fail-DeveloperPreviewRelease -Code 'DPR_OUTPUT_PARENT_INVALID'
}
Assert-DPRNoReparseAncestry -Path $outputParent -Code 'DPR_OUTPUT_REPARSE'

[void][IO.Directory]::CreateDirectory($output)
$controlParent = Join-Path $output 'control'
$outputParentForFiles = Join-Path $output 'control-outputs'
[void][IO.Directory]::CreateDirectory($controlParent)
[void][IO.Directory]::CreateDirectory($outputParentForFiles)

$artifactRoots = New-Object 'Collections.Generic.List[string]'
$artifactSetHashes = New-Object 'Collections.Generic.List[string]'
$targetRecords = New-Object 'Collections.Generic.List[object]'
$goDirectory = [IO.Path]::GetDirectoryName($go)
$savedPath = $env:PATH
$savedEnvironment = @{}
foreach ($name in @(
    'GOWORK', 'GOENV', 'GOTOOLCHAIN', 'GOFLAGS', 'GOPROXY',
    'GOCACHE', 'GOMODCACHE', 'GOTMPDIR', 'GOPATH',
    'TEMP', 'TMP', 'TMPDIR'
)) {
    $savedEnvironment[$name] =
        [Environment]::GetEnvironmentVariable($name, 'Process')
}

try {
    foreach ($target in $script:Targets) {
        $targetName = [string]$target.Target
        $baseRoot = Join-Path $controlParent $targetName
        $baseControlDirectory = Join-Path $baseRoot 'control'
        $baseControlPath = Join-Path $baseControlDirectory 'workflow-control.ps1'
        $prepareOutputPath = Join-Path $outputParentForFiles "$targetName.prepare.txt"
        $prepareEnvironmentPath = Join-Path $outputParentForFiles "$targetName.environment.txt"
        $finalizeOutputPath = Join-Path $outputParentForFiles "$targetName.finalize.txt"

        if (Test-Path -LiteralPath $baseRoot) {
            Fail-DeveloperPreviewRelease -Code 'DPR_BASE_ROOT_EXISTS'
        }
        [void][IO.Directory]::CreateDirectory($baseControlDirectory)
        [IO.File]::Copy($control, $baseControlPath, $false)

        & $baseControlPath `
            -Prepare `
            -JobKind cross-build `
            -BaseRoot $baseRoot `
            -RepositoryRoot $repository `
            -Revision $Revision `
            -GitHubOutputPath $prepareOutputPath `
            -GitHubEnvironmentPath $prepareEnvironmentPath `
            -TargetGoos ([string]$target.Goos) `
            -TargetGoarch ([string]$target.Goarch)

        $prepareOutput = Read-DPRKeyValueFile `
            -Path $prepareOutputPath `
            -Code 'DPR_PREPARE_OUTPUT_INVALID'
        foreach ($requiredKey in @(
            'base', 'source', 'stage', 'artifact', 'cache',
            'execution_temp', 'manifest', 'manifest_sha256', 'control'
        )) {
            if (-not $prepareOutput.Contains($requiredKey)) {
                Fail-DeveloperPreviewRelease -Code 'DPR_PREPARE_OUTPUT_INVALID'
            }
        }
        $manifestSha256 = [string]$prepareOutput['manifest_sha256']
        if ($manifestSha256 -cnotmatch '^[0-9a-f]{64}$') {
            Fail-DeveloperPreviewRelease -Code 'DPR_PREPARE_OUTPUT_INVALID'
        }

        $prepareEnvironment = Read-DPRKeyValueFile `
            -Path $prepareEnvironmentPath `
            -Code 'DPR_PREPARE_ENVIRONMENT_INVALID'
        foreach ($entry in $prepareEnvironment.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable(
                [string]$entry.Key,
                [string]$entry.Value,
                'Process'
            )
        }
        $env:PATH = $goDirectory + [IO.Path]::PathSeparator + $savedPath

        if (-not [string]::IsNullOrWhiteSpace($moduleCacheSeedPath)) {
            $targetModuleCache = Join-Path ([string]$prepareOutput['cache']) 'module'
            if (-not (Test-Path -LiteralPath $targetModuleCache -PathType Container)) {
                Fail-DeveloperPreviewRelease -Code 'DPR_MODULE_CACHE_TARGET_MISSING'
            }
            if (@(Get-ChildItem -LiteralPath $targetModuleCache -Force).Count -ne 0) {
                Fail-DeveloperPreviewRelease -Code 'DPR_MODULE_CACHE_TARGET_NOT_EMPTY'
            }
            try {
                if ($script:IsWindowsPlatform) {
                    & robocopy `
                        $moduleCacheSeedPath `
                        $targetModuleCache `
                        /E /COPY:DAT /DCOPY:DAT /R:2 /W:1 `
                        /NFL /NDL /NP /NJH /NJS /MT:16 | Out-Null
                    if ($LASTEXITCODE -gt 7) {
                        Fail-DeveloperPreviewRelease -Code 'DPR_MODULE_CACHE_SEED_COPY_FAILED'
                    }
                } else {
                    Get-ChildItem -LiteralPath $moduleCacheSeedPath -Force |
                        Copy-Item -Destination $targetModuleCache -Recurse -Force
                }
            } catch {
                Fail-DeveloperPreviewRelease -Code 'DPR_MODULE_CACHE_SEED_COPY_FAILED'
            }
            $env:GOPROXY = 'off'
        }

        & $baseControlPath `
            -Initialize `
            -JobKind cross-build `
            -BaseRoot $baseRoot `
            -TargetGoos ([string]$target.Goos) `
            -TargetGoarch ([string]$target.Goarch)

        & $baseControlPath `
            -Run `
            -JobKind cross-build `
            -BaseRoot $baseRoot `
            -TargetGoos ([string]$target.Goos) `
            -TargetGoarch ([string]$target.Goarch)

        & $baseControlPath `
            -Finalize `
            -JobKind cross-build `
            -BaseRoot $baseRoot `
            -ManifestSha256 $manifestSha256 `
            -GitHubOutputPath $finalizeOutputPath `
            -TargetGoos ([string]$target.Goos) `
            -TargetGoarch ([string]$target.Goarch)

        $finalizeOutput = Read-DPRKeyValueFile `
            -Path $finalizeOutputPath `
            -Code 'DPR_FINALIZE_OUTPUT_INVALID'
        foreach ($requiredKey in @(
            'artifact_set_sha256', 'artifact_file_count',
            'artifact_bytes', 'run_succeeded'
        )) {
            if (-not $finalizeOutput.Contains($requiredKey)) {
                Fail-DeveloperPreviewRelease -Code 'DPR_FINALIZE_OUTPUT_INVALID'
            }
        }
        $artifactSetSha256 = [string]$finalizeOutput['artifact_set_sha256']
        if ($artifactSetSha256 -cnotmatch '^[0-9a-f]{64}$') {
            Fail-DeveloperPreviewRelease -Code 'DPR_FINALIZE_OUTPUT_INVALID'
        }
        if ([string]$finalizeOutput['run_succeeded'] -cne 'true') {
            Fail-DeveloperPreviewRelease -Code 'DPR_RUN_NOT_SUCCESSFUL'
        }

        & $baseControlPath `
            -Seal `
            -JobKind cross-build `
            -BaseRoot $baseRoot `
            -ManifestSha256 $manifestSha256 `
            -ExpectedArtifactSetSha256 $artifactSetSha256 `
            -TargetGoos ([string]$target.Goos) `
            -TargetGoarch ([string]$target.Goarch)

        $artifactRoot = [string]$prepareOutput['artifact']
        $artifactRoots.Add($artifactRoot)
        $artifactSetHashes.Add($artifactSetSha256)
        $targetRecords.Add([pscustomobject]@{
            target = $targetName
            goos = [string]$target.Goos
            goarch = [string]$target.Goarch
            artifact_root = $artifactRoot
            artifact_set_sha256 = $artifactSetSha256
            manifest_sha256 = $manifestSha256
        })

        foreach ($entry in $savedEnvironment.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable(
                [string]$entry.Key,
                [string]$entry.Value,
                'Process'
            )
        }
        $env:PATH = $savedPath
    }

    $archiveOutputRoot = Join-Path $output 'archives'
    $archiveParameters = @{
        ArtifactRoot = $artifactRoots.ToArray()
        ExpectedArtifactSetSha256 = $artifactSetHashes.ToArray()
        Version = $Version
        Commit = $Revision
        OutputRoot = $archiveOutputRoot
    }
    if ($BuildOnly) { $archiveParameters.BuildOnly = $true }
    $archiveResults = @(& $archiveTool @archiveParameters)
    if ($archiveResults.Count -ne 1 -or $null -eq $archiveResults[0]) {
        Fail-DeveloperPreviewRelease -Code 'DPR_ARCHIVE_OUTPUT_INVALID'
    }
    $archiveResult = $archiveResults[0]
    foreach ($requiredProperty in @(
        'OutputRoot', 'ManifestPath', 'ArchiveCount'
    )) {
        if ($null -eq $archiveResult.PSObject.Properties[$requiredProperty]) {
            Fail-DeveloperPreviewRelease -Code 'DPR_ARCHIVE_OUTPUT_INVALID'
        }
    }
    $archiveOutputRootResult = [string]$archiveResult.OutputRoot
    $archiveManifestResult = [string]$archiveResult.ManifestPath
    $archiveCountResult = [int]$archiveResult.ArchiveCount
    if (-not [string]::Equals(
            $archiveOutputRootResult,
            $archiveOutputRoot,
            $script:PathComparison
        ) -or
        -not [string]::Equals(
            $archiveManifestResult,
            (Join-Path $archiveOutputRoot 'SHA256SUMS'),
            $script:PathComparison
        ) -or
        $archiveCountResult -ne 6 -or
        -not (Test-Path -LiteralPath $archiveManifestResult -PathType Leaf)) {
        Fail-DeveloperPreviewRelease -Code 'DPR_ARCHIVE_OUTPUT_INVALID'
    }

    $summary = [ordered]@{
        schema = 'freeagent.developer-preview-release/v1'
        status = 'PASS'
        repository_root = $repository
        revision = $Revision
        version = $Version
        output_root = $output
        archive_root = $archiveResult.OutputRoot
        archive_manifest = $archiveResult.ManifestPath
        archive_count = $archiveCountResult
        build_only = [bool]$BuildOnly
        module_cache_seed = $moduleCacheSeedPath
        targets = $targetRecords.ToArray()
    }
    $summaryPath = Join-Path $output 'developer-preview-release.json'
    Write-DPRSummary -Path $summaryPath -Value $summary
    Write-Host (
        'DEVELOPER_PREVIEW_RELEASE_PASS ' +
        "version=$Version revision=$Revision archives=$archiveCountResult"
    )
    [pscustomobject]$summary
} catch {
    foreach ($entry in $savedEnvironment.GetEnumerator()) {
        try {
            [Environment]::SetEnvironmentVariable(
                [string]$entry.Key,
                [string]$entry.Value,
                'Process'
            )
        } catch {
        }
    }
    $env:PATH = $savedPath
    if ($_.Exception.Message.StartsWith(
        'DEVELOPER_PREVIEW_RELEASE_FAIL code=',
        [StringComparison]::Ordinal
    )) {
        throw
    }
    Fail-DeveloperPreviewRelease -Code 'DPR_UNEXPECTED_FAILURE'
}
