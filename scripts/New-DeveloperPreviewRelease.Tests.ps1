#requires -Version 7.2

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Tool = Join-Path $PSScriptRoot 'New-DeveloperPreviewRelease.ps1'
$script:Utf8NoBom = [Text.UTF8Encoding]::new($false)
$script:Cases = 0
$script:Failures = [Collections.Generic.List[string]]::new()

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    if (-not $Condition) { throw "ASSERT_FAIL $Name" }
}

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()]$Expected,
        [AllowNull()]$Actual
    )
    if ($Expected -cne $Actual) {
        throw "ASSERT_FAIL $Name expected=[$Expected] actual=[$Actual]"
    }
}

function Invoke-TestCase {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $script:Cases++
    try {
        & $Body
        Write-Host "PASS $Name"
    } catch {
        $script:Failures.Add("$Name`: $($_.Exception.Message)")
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
    }
}

function Assert-ThrowsCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $message = ''
    try {
        & $Body
    } catch {
        $message = $_.Exception.Message
    }
    Assert-True -Name "$Name throws" -Condition (
        -not [string]::IsNullOrWhiteSpace($message)
    )
    Assert-True -Name "$Name exact code" -Condition (
        $message.StartsWith(
            "DEVELOPER_PREVIEW_RELEASE_FAIL code=$Code",
            [StringComparison]::Ordinal
        )
    )
}

function Write-TestText {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [IO.Directory]::Exists($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function New-TestRepository {
    param([Parameter(Mandatory = $true)][string]$Path)

    [void][IO.Directory]::CreateDirectory($Path)
    & git -C $Path init --quiet
    if ($LASTEXITCODE -ne 0) { throw 'git init failed' }
    & git -C $Path config user.name 'FreeAgent Test'
    if ($LASTEXITCODE -ne 0) { throw 'git config user.name failed' }
    & git -C $Path config user.email 'freeagent-test@example.invalid'
    if ($LASTEXITCODE -ne 0) { throw 'git config user.email failed' }
    Write-TestText -Path (Join-Path $Path 'README.md') -Text "# test`n"
    & git -C $Path add README.md
    if ($LASTEXITCODE -ne 0) { throw 'git add failed' }
    & git -C $Path commit --quiet -m 'test'
    if ($LASTEXITCODE -ne 0) { throw 'git commit failed' }
    $revision = (& git -C $Path rev-parse HEAD).Trim()
    return [pscustomobject]@{
        Path = $Path
        Revision = $revision
    }
}

function New-FakeControl {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$LogPath
    )

    $source = @'
[CmdletBinding()]
param(
    [switch]$Prepare,
    [switch]$Initialize,
    [switch]$Run,
    [switch]$Finalize,
    [switch]$Seal,
    [string]$JobKind = '',
    [string]$BaseRoot = '',
    [string]$RepositoryRoot = '',
    [string]$Revision = '',
    [string]$GitHubOutputPath = '',
    [string]$GitHubEnvironmentPath = '',
    [string]$ManifestSha256 = '',
    [string]$ExpectedArtifactSetSha256 = '',
    [string]$TargetGoos = '',
    [string]$TargetGoarch = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$logPath = 'LOG_PATH'
$expectedSelf = Join-Path $BaseRoot 'control/workflow-control.ps1'
if (-not [string]::Equals(
        $PSCommandPath,
        $expectedSelf,
        [StringComparison]::OrdinalIgnoreCase
    )) {
    throw 'FAKE_CONTROL_WRONG_SELF'
}
function Append-Log([string]$Text) {
    [IO.File]::AppendAllText($logPath, $Text + "`n", [Text.UTF8Encoding]::new($false))
}

if ($Prepare) {
    Append-Log ('prepare|' + $JobKind + '|' + $BaseRoot + '|' + $RepositoryRoot + '|' + $Revision + '|' + $TargetGoos + '|' + $TargetGoarch)
    [void][IO.Directory]::CreateDirectory($BaseRoot)
    $work = Join-Path $BaseRoot 'trusted/work'
    $source = Join-Path $work 'source'
    $stage = Join-Path $work 'stage'
    $artifact = Join-Path $work 'artifact'
    $cache = Join-Path $work 'cache'
    $moduleCache = Join-Path $cache 'module'
    $executionTemp = Join-Path $BaseRoot 'trusted/execution'
    foreach ($directory in @($source, $stage, $artifact, $cache, $moduleCache, $executionTemp)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    $manifest = Join-Path $artifact 'public-tree-manifest.v1.json'
    [IO.File]::WriteAllText($manifest, "{}`n", [Text.UTF8Encoding]::new($false))
    $control = Join-Path $BaseRoot 'control/workflow-control.ps1'
    $output = @(
        "base=$BaseRoot",
        "source=$source",
        "stage=$stage",
        "artifact=$artifact",
        "cache=$cache",
        "execution_temp=$executionTemp",
        "manifest=$manifest",
        "manifest_sha256=1111111111111111111111111111111111111111111111111111111111111111",
        "control=$control"
    ) -join "`n"
    [IO.File]::WriteAllText($GitHubOutputPath, $output + "`n", [Text.UTF8Encoding]::new($false))
    $environment = @(
        'GOWORK=off',
        'GOENV=off',
        'GOTOOLCHAIN=local',
        'GOFLAGS=-mod=readonly -buildvcs=false',
        "GOCACHE=$(Join-Path $cache 'build')",
        "GOMODCACHE=$(Join-Path $cache 'module')",
        "GOTMPDIR=$(Join-Path $cache 'go-tmp')",
        "GOPATH=$(Join-Path $cache 'gopath')",
        "TEMP=$executionTemp",
        "TMP=$executionTemp",
        "TMPDIR=$executionTemp"
    ) -join "`n"
    [IO.File]::WriteAllText($GitHubEnvironmentPath, $environment + "`n", [Text.UTF8Encoding]::new($false))
    Write-Host 'FAKE_CONTROL_PREPARE_PASS'
    return
}

if ($Initialize) {
    Append-Log ('initialize|' + $JobKind + '|' + $BaseRoot + '|' + $TargetGoos + '|' + $TargetGoarch)
    Write-Host 'FAKE_CONTROL_INITIALIZE_PASS'
    return
}

if ($Run) {
    Append-Log ('run|' + $JobKind + '|' + $BaseRoot + '|' + $TargetGoos + '|' + $TargetGoarch + '|GOPROXY=' + $env:GOPROXY)
    Write-Host 'FAKE_CONTROL_RUN_PASS'
    return
}

if ($Finalize) {
    Append-Log ('finalize|' + $JobKind + '|' + $BaseRoot + '|' + $ManifestSha256 + '|' + $TargetGoos + '|' + $TargetGoarch)
    $output = @(
        'artifact_set_sha256=2222222222222222222222222222222222222222222222222222222222222222',
        'artifact_file_count=25',
        'artifact_bytes=123456',
        'run_succeeded=true'
    ) -join "`n"
    [IO.File]::WriteAllText($GitHubOutputPath, $output + "`n", [Text.UTF8Encoding]::new($false))
    Write-Host 'FAKE_CONTROL_FINALIZE_PASS'
    return
}

if ($Seal) {
    Append-Log ('seal|' + $JobKind + '|' + $BaseRoot + '|' + $ManifestSha256 + '|' + $ExpectedArtifactSetSha256 + '|' + $TargetGoos + '|' + $TargetGoarch)
    Write-Host 'FAKE_CONTROL_SEAL_PASS'
    return
}

throw 'FAKE_CONTROL_INVALID_PHASE'
'@
    $source = $source.Replace('LOG_PATH', $LogPath.Replace("'", "''"))
    Write-TestText -Path $Path -Text $source
}

function New-FakeGoCommand {
    param(
        [Parameter(Mandatory = $true)][string]$Path
    )

    Write-TestText -Path $Path -Text "fake go command for test`n"
}

function New-FakeArchiveTool {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$LogPath,
        [ValidateSet('valid', 'wrong-count')]
        [string]$Mode = 'valid'
    )

    $source = @'
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string[]]$ArtifactRoot,
    [Parameter(Mandatory = $true)][string[]]$ExpectedArtifactSetSha256,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Commit,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [switch]$BuildOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$logPath = 'LOG_PATH'
$record = [ordered]@{
    artifact_roots = $ArtifactRoot
    artifact_set_hashes = $ExpectedArtifactSetSha256
    version = $Version
    commit = $Commit
    output_root = $OutputRoot
    build_only = [bool]$BuildOnly
}
[IO.File]::WriteAllText(
    $logPath,
    (($record | ConvertTo-Json -Depth 8) + "`n"),
    [Text.UTF8Encoding]::new($false)
)
[void][IO.Directory]::CreateDirectory($OutputRoot)
$manifestPath = Join-Path $OutputRoot 'SHA256SUMS'
[IO.File]::WriteAllText($manifestPath, "fake`n", [Text.UTF8Encoding]::new($false))
Write-Host 'FAKE_ARCHIVE_TOOL_PASS'
[pscustomobject]@{
    OutputRoot = $OutputRoot
    ManifestPath = $manifestPath
    ArchiveCount = ARCHIVE_COUNT
    BuildOnly = [bool]$BuildOnly
}
'@
    $source = $source.Replace('LOG_PATH', $LogPath.Replace("'", "''"))
    $source = $source.Replace('ARCHIVE_COUNT', $(if ($Mode -ceq 'wrong-count') { '5' } else { '6' }))
    Write-TestText -Path $Path -Text $source
}

$suiteRoot = Join-Path ([IO.Path]::GetTempPath()) (
    'freeagent-developer-preview-release-tests-' +
    [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)
try {
    Invoke-TestCase -Name 'parser and parameter contracts' -Body {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Tool,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parser errors' -Expected 0 -Actual @($errors).Count
        $source = [IO.File]::ReadAllText($script:Tool)
        Assert-True -Name 'six target contract' -Condition (
            $source.Contains("'windows-amd64'") -and
            $source.Contains("'windows-arm64'") -and
            $source.Contains("'linux-amd64'") -and
            $source.Contains("'linux-arm64'") -and
            $source.Contains("'darwin-amd64'") -and
            $source.Contains("'darwin-arm64'")
        )
        Assert-True -Name 'control integration contract' -Condition (
            $source.Contains('Invoke-CIWorkflowControl.ps1')
        )
        Assert-True -Name 'archive integration contract' -Condition (
            $source.Contains('New-DeveloperPreviewArchives.ps1')
        )
        Assert-True -Name 'module cache seed contract' -Condition (
            $source.Contains('ModuleCacheSeed')
        )
    }

    Invoke-TestCase -Name 'happy path orchestrates six targets and one archive call' -Body {
        $caseRoot = Join-Path $suiteRoot 'happy'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        $controlPath = Join-Path $caseRoot 'fake-control.ps1'
        $archiveToolPath = Join-Path $caseRoot 'fake-archive.ps1'
        $controlLog = Join-Path $caseRoot 'control.log'
        $archiveLog = Join-Path $caseRoot 'archive.log'
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        New-FakeControl -Path $controlPath -LogPath $controlLog
        New-FakeArchiveTool -Path $archiveToolPath -LogPath $archiveLog
        New-FakeGoCommand -Path $goCommand

        $result = & $script:Tool `
            -RepositoryRoot $repository.Path `
            -Revision $repository.Revision `
            -Version 'v0.1.0-dev.1' `
            -OutputRoot $outputRoot `
            -GoCommand $goCommand `
            -ControlPath $controlPath `
            -ArchiveToolPath $archiveToolPath

        Assert-Equal -Name 'summary status' -Expected 'PASS' -Actual $result.status
        Assert-Equal -Name 'summary archive count' -Expected 6 -Actual $result.archive_count
        Assert-Equal -Name 'target record count' -Expected 6 -Actual @($result.targets).Count
        Assert-True -Name 'summary file exists' -Condition (
            [IO.File]::Exists((Join-Path $outputRoot 'developer-preview-release.json'))
        )
        foreach ($targetName in @(
            'windows-amd64',
            'windows-arm64',
            'linux-amd64',
            'linux-arm64',
            'darwin-amd64',
            'darwin-arm64'
        )) {
            $snapshot = Join-Path $outputRoot (
                "control/$targetName/control/workflow-control.ps1"
            )
            Assert-True -Name "$targetName control snapshot exists" -Condition (
                [IO.File]::Exists($snapshot)
            )
            Assert-Equal -Name "$targetName control snapshot bytes" `
                -Expected ([IO.File]::ReadAllText($controlPath)) `
                -Actual ([IO.File]::ReadAllText($snapshot))
        }

        [string[]]$controlLines = @(
            [IO.File]::ReadAllLines($controlLog)
        )
        Assert-Equal -Name 'prepare invocation count' `
            -Expected 6 `
            -Actual @($controlLines | Where-Object { $_.StartsWith('prepare|') }).Count
        Assert-Equal -Name 'initialize invocation count' `
            -Expected 6 `
            -Actual @($controlLines | Where-Object { $_.StartsWith('initialize|') }).Count
        Assert-Equal -Name 'run invocation count' `
            -Expected 6 `
            -Actual @($controlLines | Where-Object { $_.StartsWith('run|') }).Count
        Assert-Equal -Name 'finalize invocation count' `
            -Expected 6 `
            -Actual @($controlLines | Where-Object { $_.StartsWith('finalize|') }).Count
        Assert-Equal -Name 'seal invocation count' `
            -Expected 6 `
            -Actual @($controlLines | Where-Object { $_.StartsWith('seal|') }).Count

        $archiveRecord = [IO.File]::ReadAllText($archiveLog) | ConvertFrom-Json
        Assert-Equal -Name 'archive root count' `
            -Expected 6 `
            -Actual @($archiveRecord.artifact_roots).Count
        Assert-Equal -Name 'archive hash count' `
            -Expected 6 `
            -Actual @($archiveRecord.artifact_set_hashes).Count
        Assert-Equal -Name 'archive version' `
            -Expected 'v0.1.0-dev.1' `
            -Actual $archiveRecord.version
        Assert-Equal -Name 'archive commit' `
            -Expected $repository.Revision `
            -Actual $archiveRecord.commit
    }

    Invoke-TestCase -Name 'module cache seed populates targets and forces offline mode' -Body {
        $caseRoot = Join-Path $suiteRoot 'module-cache-seed'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        $controlPath = Join-Path $caseRoot 'fake-control.ps1'
        $archiveToolPath = Join-Path $caseRoot 'fake-archive.ps1'
        $controlLog = Join-Path $caseRoot 'control.log'
        $archiveLog = Join-Path $caseRoot 'archive.log'
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        $seedRoot = Join-Path $caseRoot 'module-cache-seed'
        New-FakeControl -Path $controlPath -LogPath $controlLog
        New-FakeArchiveTool -Path $archiveToolPath -LogPath $archiveLog
        New-FakeGoCommand -Path $goCommand
        [void][IO.Directory]::CreateDirectory($seedRoot)
        Write-TestText -Path (Join-Path $seedRoot 'marker.txt') -Text "seed`n"

        $result = & $script:Tool `
            -RepositoryRoot $repository.Path `
            -Revision $repository.Revision `
            -Version 'v0.1.0-dev.1' `
            -OutputRoot $outputRoot `
            -GoCommand $goCommand `
            -ControlPath $controlPath `
            -ArchiveToolPath $archiveToolPath `
            -ModuleCacheSeed $seedRoot

        Assert-Equal -Name 'summary module cache seed' `
            -Expected $seedRoot `
            -Actual $result.module_cache_seed
        foreach ($targetName in @(
            'windows-amd64',
            'windows-arm64',
            'linux-amd64',
            'linux-arm64',
            'darwin-amd64',
            'darwin-arm64'
        )) {
            $marker = Join-Path $outputRoot (
                "control/$targetName/trusted/work/cache/module/marker.txt"
            )
            Assert-True -Name "$targetName module cache seed marker" -Condition (
                [IO.File]::Exists($marker)
            )
        }

        [string[]]$runLines = @(
            [IO.File]::ReadAllLines($controlLog) |
                Where-Object { $_.StartsWith('run|') }
        )
        Assert-Equal -Name 'offline run count' `
            -Expected 6 `
            -Actual @($runLines | Where-Object { $_.EndsWith('|GOPROXY=off') }).Count
    }

    Invoke-TestCase -Name 'invalid version fails closed' -Body {
        $caseRoot = Join-Path $suiteRoot 'invalid-version'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        Assert-ThrowsCode -Name 'invalid version' -Code 'DPR_VERSION_INVALID' -Body {
            & $script:Tool `
                -RepositoryRoot $repository.Path `
                -Revision $repository.Revision `
                -Version '0.1.0' `
                -OutputRoot $outputRoot `
                -GoCommand $goCommand
        }
    }

    Invoke-TestCase -Name 'existing output fails without replacement' -Body {
        $caseRoot = Join-Path $suiteRoot 'existing-output'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        New-FakeGoCommand -Path $goCommand
        [void][IO.Directory]::CreateDirectory($outputRoot)
        $sentinel = Join-Path $outputRoot 'sentinel.txt'
        Write-TestText -Path $sentinel -Text 'keep'
        Assert-ThrowsCode -Name 'existing output' -Code 'DPR_OUTPUT_EXISTS' -Body {
            & $script:Tool `
                -RepositoryRoot $repository.Path `
                -Revision $repository.Revision `
                -Version 'v0.1.0-dev.1' `
                -OutputRoot $outputRoot `
                -GoCommand $goCommand
        }
        Assert-Equal -Name 'existing output unchanged' `
            -Expected 'keep' `
            -Actual ([IO.File]::ReadAllText($sentinel))
    }

    Invoke-TestCase -Name 'dirty repository fails closed' -Body {
        $caseRoot = Join-Path $suiteRoot 'dirty-repository'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        Write-TestText -Path (Join-Path $repository.Path 'dirty.txt') -Text 'dirty'
        & git -C $repository.Path add dirty.txt
        if ($LASTEXITCODE -ne 0) { throw 'git add dirty file failed' }
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        New-FakeGoCommand -Path $goCommand
        Assert-ThrowsCode -Name 'dirty repository' -Code 'DPR_REPOSITORY_DIRTY' -Body {
            & $script:Tool `
                -RepositoryRoot $repository.Path `
                -Revision $repository.Revision `
                -Version 'v0.1.0-dev.1' `
                -OutputRoot $outputRoot `
                -GoCommand $goCommand
        }
    }

    Invoke-TestCase -Name 'non-git repository fails before output creation' -Body {
        $caseRoot = Join-Path $suiteRoot 'non-git'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        [void][IO.Directory]::CreateDirectory($repositoryRoot)
        Write-TestText -Path (Join-Path $repositoryRoot 'README.md') -Text '# not git'
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        New-FakeGoCommand -Path $goCommand
        Assert-ThrowsCode -Name 'non-git repository' -Code 'DPR_GIT_STATUS_FAILED' -Body {
            & $script:Tool `
                -RepositoryRoot $repositoryRoot `
                -Revision ('0' * 40) `
                -Version 'v0.1.0-dev.1' `
                -OutputRoot $outputRoot `
                -GoCommand $goCommand
        }
        Assert-True -Name 'non-git output absent' -Condition (
            -not (Test-Path -LiteralPath $outputRoot)
        )
    }

    Invoke-TestCase -Name 'invalid archive result fails closed' -Body {
        $caseRoot = Join-Path $suiteRoot 'invalid-archive-result'
        $repositoryRoot = Join-Path $caseRoot 'repository'
        $repository = New-TestRepository -Path $repositoryRoot
        $controlPath = Join-Path $caseRoot 'fake-control.ps1'
        $archiveToolPath = Join-Path $caseRoot 'fake-archive.ps1'
        $controlLog = Join-Path $caseRoot 'control.log'
        $archiveLog = Join-Path $caseRoot 'archive.log'
        $outputRoot = Join-Path $caseRoot 'output'
        $goCommand = Join-Path $caseRoot 'go.cmd'
        New-FakeControl -Path $controlPath -LogPath $controlLog
        New-FakeArchiveTool -Path $archiveToolPath -LogPath $archiveLog -Mode wrong-count
        New-FakeGoCommand -Path $goCommand

        Assert-ThrowsCode -Name 'wrong archive count' -Code 'DPR_ARCHIVE_OUTPUT_INVALID' -Body {
            & $script:Tool `
                -RepositoryRoot $repository.Path `
                -Revision $repository.Revision `
                -Version 'v0.1.0-dev.1' `
                -OutputRoot $outputRoot `
                -GoCommand $goCommand `
                -ControlPath $controlPath `
                -ArchiveToolPath $archiveToolPath
        }
        Assert-True -Name 'summary absent after invalid archive result' -Condition (
            -not [IO.File]::Exists((Join-Path $outputRoot 'developer-preview-release.json'))
        )
    }
} finally {
    if ([IO.Directory]::Exists($suiteRoot)) {
        $suiteRootFull = [IO.Path]::GetFullPath($suiteRoot)
        $tempRootFull = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
        if ($suiteRootFull.StartsWith(
                $tempRootFull,
                [StringComparison]::OrdinalIgnoreCase
            )) {
            Remove-Item -LiteralPath $suiteRootFull -Recurse -Force
        } else {
            throw "TEST_TEMP_ROOT_UNEXPECTED path=$suiteRootFull"
        }
    }
}

if ($script:Failures.Count -gt 0) {
    foreach ($failure in $script:Failures) { Write-Error $failure }
    throw "DEVELOPER_PREVIEW_RELEASE_TEST_FAIL failures=$($script:Failures.Count)"
}
Write-Host (
    'DEVELOPER_PREVIEW_RELEASE_TEST_PASS cases=' +
    $script:Cases
)
