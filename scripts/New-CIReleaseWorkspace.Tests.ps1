[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Bootstrap = Join-Path $PSScriptRoot 'New-CIReleaseWorkspace.ps1'
$script:Generator = Join-Path $PSScriptRoot 'New-PublicStaging.ps1'
$script:Verifier = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Git = (@(
    Get-Command git -CommandType Application -ErrorAction Stop
))[0].Source
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:MaximumChildMilliseconds = 600000
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'System.Collections.Generic.List[string]'

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:Assertions++
    if (-not $Condition) {
        throw "ASSERT_FAIL $Name"
    }
}

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()]$Expected,
        [AllowNull()]$Actual
    )
    $script:Assertions++
    if ($Expected -cne $Actual) {
        throw "ASSERT_FAIL $Name expected=[$Expected] actual=[$Actual]"
    }
}

function Invoke-Case {
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

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function Invoke-Git {
    param(
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    $output = New-Object 'System.Collections.Generic.List[string]'
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $script:Git -C $Repository @Arguments 2>&1 |
            ForEach-Object { [void]$output.Add([string]$_) }
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    if ($exitCode -ne 0) {
        throw "fixture git failed exit=$exitCode argv=$($Arguments -join ' ') output=$($output -join ' | ')"
    }
    return ($output -join "`n").Trim()
}

function Commit-All {
    param(
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string]$Message
    )
    [void](Invoke-Git -Repository $Repository -Arguments @('add', '--all'))
    [void](Invoke-Git -Repository $Repository -Arguments @(
        '-c', 'commit.gpgsign=false',
        'commit', '--quiet', '--no-verify', '-m', $Message
    ))
    return Invoke-Git -Repository $Repository -Arguments @('rev-parse', 'HEAD')
}

function New-GitFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name,
        [switch]$Dirty
    )
    $root = Join-Path $Container $Name
    $repository = Join-Path $root (([char]0x4ED3).ToString() + ([char]0x5E93).ToString() + ' with space')
    $work = Join-Path $root 'release work'
    [void][IO.Directory]::CreateDirectory($repository)
    [void](Invoke-Git -Repository $repository -Arguments @('init', '--quiet'))
    [void](Invoke-Git -Repository $repository -Arguments @('config', 'user.name', 'FreeAgent Test'))
    [void](Invoke-Git -Repository $repository -Arguments @('config', 'user.email', 'freeagent-test@example.invalid'))
    [void](Invoke-Git -Repository $repository -Arguments @('config', 'core.autocrlf', 'false'))
    [void](Invoke-Git -Repository $repository -Arguments @('config', 'core.filemode', 'true'))

    [void][IO.Directory]::CreateDirectory((Join-Path $repository 'scripts'))
    [IO.File]::Copy($script:Generator, (Join-Path $repository 'scripts/New-PublicStaging.ps1'), $false)
    [IO.File]::Copy($script:Verifier, (Join-Path $repository 'scripts/Test-PublicStaging.ps1'), $false)
    Write-Utf8NoBom -Path (Join-Path $repository 'README.md') -Text "# committed bytes`n"
    Write-Utf8NoBom `
        -Path (Join-Path $repository (([char]0x8D44).ToString() + ([char]0x6599).ToString() + '/file with space.txt')) `
        -Text "unicode path`n"
    $revision = Commit-All -Repository $repository -Message 'fixture'

    if ($Dirty) {
        Write-Utf8NoBom -Path (Join-Path $repository 'README.md') -Text "# dirty bytes must not escape`n"
        Write-Utf8NoBom -Path (Join-Path $repository 'untracked-secret.txt') -Text "never archive this`n"
    }
    return [pscustomobject]@{
        Root = $root
        Repository = $repository
        Work = $work
        Revision = $revision
    }
}

function ConvertTo-Utf8Base64 {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Value
    )
    return [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($Value))
}

function Invoke-Bootstrap {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [string]$Revision = $Fixture.Revision,
        [string]$WorkRoot = $Fixture.Work,
        [string]$ExpectedGeneratorSha256 = '',
        [string]$ExpectedVerifierSha256 = ''
    )
    if ([string]::IsNullOrWhiteSpace($ExpectedGeneratorSha256)) {
        $ExpectedGeneratorSha256 = (
            Get-FileHash `
                -LiteralPath (Join-Path $Fixture.Repository 'scripts/New-PublicStaging.ps1') `
                -Algorithm SHA256
        ).Hash.ToLowerInvariant()
    }
    if ([string]::IsNullOrWhiteSpace($ExpectedVerifierSha256)) {
        $ExpectedVerifierSha256 = (
            Get-FileHash `
                -LiteralPath (Join-Path $Fixture.Repository 'scripts/Test-PublicStaging.ps1') `
                -Algorithm SHA256
        ).Hash.ToLowerInvariant()
    }
    $bootstrapPath = ConvertTo-Utf8Base64 -Value $script:Bootstrap
    $repositoryRoot = ConvertTo-Utf8Base64 -Value $Fixture.Repository
    $revisionValue = ConvertTo-Utf8Base64 -Value $Revision
    $workRootValue = ConvertTo-Utf8Base64 -Value $WorkRoot
    $generatorSha256 = ConvertTo-Utf8Base64 -Value $ExpectedGeneratorSha256
    $verifierSha256 = ConvertTo-Utf8Base64 -Value $ExpectedVerifierSha256
    $bootstrapCommand = @(
        '$utf8 = New-Object Text.UTF8Encoding($false)',
        '[Console]::OutputEncoding = $utf8',
        (
            'try { $bufferSize = $Host.UI.RawUI.BufferSize; ' +
                'if ($bufferSize.Width -lt 4096) { $bufferSize.Width = 4096; ' +
                '$Host.UI.RawUI.BufferSize = $bufferSize } } catch {}'
        ),
        "`$scriptPath = `$utf8.GetString([Convert]::FromBase64String('$bootstrapPath'))",
        "`$repository = `$utf8.GetString([Convert]::FromBase64String('$repositoryRoot'))",
        "`$revision = `$utf8.GetString([Convert]::FromBase64String('$revisionValue'))",
        "`$work = `$utf8.GetString([Convert]::FromBase64String('$workRootValue'))",
        "`$generator = `$utf8.GetString([Convert]::FromBase64String('$generatorSha256'))",
        "`$verifier = `$utf8.GetString([Convert]::FromBase64String('$verifierSha256'))",
        (
            '& $scriptPath -RepositoryRoot $repository -Revision $revision -WorkRoot $work ' +
                '-ExpectedGeneratorSha256 $generator -ExpectedVerifierSha256 $verifier'
        ),
        'if ($?) { exit 0 }',
        'exit 1'
    ) -join "`n"

    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $script:PowerShell
    $start.Arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command -'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    if ($null -ne $start.PSObject.Properties['StandardOutputEncoding']) {
        $start.StandardOutputEncoding = $script:Utf8NoBom
    }
    if ($null -ne $start.PSObject.Properties['StandardErrorEncoding']) {
        $start.StandardErrorEncoding = $script:Utf8NoBom
    }

    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    $standardOutputTask = $null
    $standardErrorTask = $null
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
            throw 'bootstrap child did not start'
        }
        $standardOutputTask = $process.StandardOutput.ReadToEndAsync()
        $standardErrorTask = $process.StandardError.ReadToEndAsync()
        $process.StandardInput.Write($bootstrapCommand)
        $process.StandardInput.Close()
        $remaining = [int64]$script:MaximumChildMilliseconds - $stopwatch.ElapsedMilliseconds
        if ($remaining -le 0 -or
            -not $process.WaitForExit([int][Math]::Min($remaining, [int]::MaxValue))) {
            try { $process.Kill() } catch {}
            throw 'bootstrap child timed out'
        }
        foreach ($capture in @($standardOutputTask, $standardErrorTask)) {
            $remaining = [int64]$script:MaximumChildMilliseconds - $stopwatch.ElapsedMilliseconds
            if ($remaining -le 0 -or
                -not $capture.Wait([int][Math]::Min($remaining, [int]::MaxValue))) {
                try { $process.Kill() } catch {}
                throw 'bootstrap child output capture timed out'
            }
            if ($capture.IsCanceled -or $capture.IsFaulted) {
                throw 'bootstrap child output capture failed'
            }
        }
        $exitCode = $process.ExitCode
        $standardOutput = [string]$standardOutputTask.Result
        $standardError = [string]$standardErrorTask.Result
        $output = @($standardOutput, $standardError) -join [Environment]::NewLine
    } finally {
        try {
            if (-not $process.HasExited) { $process.Kill() }
        } catch {}
        try { $process.Dispose() } catch {}
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = $output
        StandardOutput = $standardOutput
        StandardError = $standardError
    }
}

function Assert-FailsWithCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-True -Name "$Name exits nonzero" -Condition ($Result.ExitCode -ne 0)
    if ($Result.StandardError.IndexOf(
        "CI_RELEASE_WORKSPACE_FAIL code=$Code",
        [StringComparison]::Ordinal
    ) -lt 0) {
        throw "ASSERT_FAIL $Name reports exact code expected=$Code output=$($Result.Output)"
    }
    $script:Assertions++
    Assert-True -Name "$Name does not report success" `
        -Condition ($Result.Output.IndexOf('CI_RELEASE_WORKSPACE_PASS ', [StringComparison]::Ordinal) -lt 0)
}

function Assert-ByteEqualTrees {
    param(
        [Parameter(Mandatory = $true)][string]$ExpectedRoot,
        [Parameter(Mandatory = $true)][string]$ActualRoot
    )
    $expected = @(Get-ChildItem -LiteralPath $ExpectedRoot -File -Recurse -Force |
        ForEach-Object {
            $relative = $_.FullName.Substring($ExpectedRoot.Length).
                TrimStart([char]92, [char]47).
                Replace([char]92, [char]47)
            "$relative`t$($_.Length)`t$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)"
        } | Sort-Object)
    $actual = @(Get-ChildItem -LiteralPath $ActualRoot -File -Recurse -Force |
        ForEach-Object {
            $relative = $_.FullName.Substring($ActualRoot.Length).
                TrimStart([char]92, [char]47).
                Replace([char]92, [char]47)
            "$relative`t$($_.Length)`t$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)"
        } | Sort-Object)
    Assert-Equal -Name 'trees have exact file bytes' -Expected ($expected -join "`n") -Actual ($actual -join "`n")
}

foreach ($required in @($script:Bootstrap, $script:Generator, $script:Verifier)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "CI_RELEASE_WORKSPACE_SELFTEST_DEPENDENCY_MISSING path=$required"
    }
}
if (-not [IO.Path]::IsPathRooted($script:PowerShell) -or -not [IO.Path]::IsPathRooted($script:Git)) {
    throw 'CI_RELEASE_WORKSPACE_SELFTEST_TOOL_PATH_INVALID'
}

$bootstrapTokens = $null
$bootstrapParseErrors = $null
$bootstrapAst = [Management.Automation.Language.Parser]::ParseFile(
    $script:Bootstrap,
    [ref]$bootstrapTokens,
    [ref]$bootstrapParseErrors
)
if ($bootstrapParseErrors.Count -ne 0) {
    throw "CI_RELEASE_WORKSPACE_SELFTEST_BOOTSTRAP_PARSE_FAIL errors=$($bootstrapParseErrors -join '; ')"
}
foreach ($functionName in @(
    'Fail-CIReleaseWorkspace',
    'Assert-PortableRelativePath',
    'Resolve-ArchiveOperationFailure',
    'Get-ChildRemainingMilliseconds',
    'Stop-BoundedChildProcess',
    'Wait-ChildTaskWithinDeadline',
    'Wait-BoundedChildProcess'
)) {
    $functionAst = @($bootstrapAst.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            $node.Name -ceq $functionName
    }, $true))
    if ($functionAst.Count -ne 1) {
        throw "CI_RELEASE_WORKSPACE_SELFTEST_FUNCTION_COUNT name=$functionName count=$($functionAst.Count)"
    }
    . ([scriptblock]::Create($functionAst[0].Extent.Text))
}

$testTempRoot = if (-not [string]::IsNullOrWhiteSpace($env:FREEAGENT_CI_WORKSPACE_TEST_TEMP_ROOT)) {
    if (-not [IO.Path]::IsPathRooted($env:FREEAGENT_CI_WORKSPACE_TEST_TEMP_ROOT) -or
        -not (Test-Path -LiteralPath $env:FREEAGENT_CI_WORKSPACE_TEST_TEMP_ROOT -PathType Container)) {
        throw 'FREEAGENT_CI_WORKSPACE_TEST_TEMP_ROOT must be an existing absolute directory.'
    }
    [IO.Path]::GetFullPath($env:FREEAGENT_CI_WORKSPACE_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}
$suiteRoot = Join-Path $testTempRoot ('freeagent-ci-release-workspace-tests-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    Invoke-Case 'portable path validator rejects traversal and collision primitives' {
        $script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
        $invalidPaths = @(
            '../escape',
            '/absolute',
            ('C:' + '/drive'),
            'two//segments',
            'back\slash',
            './dot',
            'CON.txt',
            '.git/config',
            'trailing./file',
            ('control' + [char]10 + 'name'),
            ('e' + [char]0x0301 + '.txt')
        )
        foreach ($invalidPath in $invalidPaths) {
            $rejected = $false
            try {
                Assert-PortableRelativePath -RelativePath $invalidPath
            } catch {
                $rejected = $_.Exception.Message.StartsWith(
                    'CI_RELEASE_WORKSPACE_FAIL code=CRW_PATH_INVALID',
                    [StringComparison]::Ordinal
                )
            }
            Assert-True -Name "invalid portable path is rejected: $invalidPath" -Condition $rejected
        }
        Assert-PortableRelativePath -RelativePath (
            ([char]0x8D44).ToString() + ([char]0x6599).ToString() + '/file with space.txt'
        )
        $script:Assertions++
    }

    Invoke-Case 'archive primary failure is never replaced by cleanup failure' {
        $primaryFailure = $null
        try {
            throw 'PRIMARY_ARCHIVE_FAILURE_SENTINEL'
        } catch {
            $primaryFailure = $_
        }
        $observed = $null
        try {
            Resolve-ArchiveOperationFailure `
                -PrimaryFailure $primaryFailure `
                -CleanupFailed $true
        } catch {
            $observed = $_.Exception.Message
        }
        Assert-Equal `
            -Name 'primary archive failure wins' `
            -Expected 'PRIMARY_ARCHIVE_FAILURE_SENTINEL' `
            -Actual $observed

        $cleanupObserved = $null
        try {
            Resolve-ArchiveOperationFailure -PrimaryFailure $null -CleanupFailed $true
        } catch {
            $cleanupObserved = $_.Exception.Message
        }
        Assert-Equal `
            -Name 'cleanup failure is terminal without a primary' `
            -Expected 'CI_RELEASE_WORKSPACE_FAIL code=CRW_ARCHIVE_CLEANUP_FAILED' `
            -Actual $cleanupObserved
    }

    Invoke-Case 'child I/O tasks cannot outlive the shared deadline' {
        $savedMaximum = $script:MaximumChildMilliseconds
        $process = $null
        try {
            $script:MaximumChildMilliseconds = 250
            $start = New-Object Diagnostics.ProcessStartInfo
            $start.FileName = $script:PowerShell
            $start.Arguments = '-NoLogo -NoProfile -NonInteractive -Command "exit 0"'
            $start.UseShellExecute = $false
            $start.CreateNoWindow = $true
            $start.RedirectStandardOutput = $true
            $start.RedirectStandardError = $true
            $process = New-Object Diagnostics.Process
            $process.StartInfo = $start
            [void]$process.Start()
            $clock = [Diagnostics.Stopwatch]::StartNew()
            $never = New-Object 'Threading.Tasks.TaskCompletionSource[object]'
            $observed = $null
            try {
                [void](Wait-BoundedChildProcess `
                    -Process $process `
                    -Stopwatch $clock `
                    -StandardOutputTask $never.Task `
                    -StandardErrorTask $never.Task `
                    -TimeoutCode 'CRW_CHILD_TIMEOUT' `
                    -CaptureFailureCode 'CRW_CHILD_CAPTURE_FAILED')
            } catch {
                $observed = $_.Exception.Message
            }
            Assert-Equal `
                -Name 'unfinished capture task reports timeout' `
                -Expected 'CI_RELEASE_WORKSPACE_FAIL code=CRW_CHILD_TIMEOUT' `
                -Actual $observed
            Assert-True `
                -Name 'unfinished capture task respects a bounded wall clock' `
                -Condition ($clock.ElapsedMilliseconds -lt 5000)
        } finally {
            $script:MaximumChildMilliseconds = $savedMaximum
            if ($null -ne $process) {
                try {
                    if (-not $process.HasExited) { $process.Kill() }
                } catch {}
                try { $process.Dispose() } catch {}
            }
        }
    }

    Invoke-Case 'fixed commit excludes dirty and untracked checkout state' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'fixed commit' -Dirty
        $result = Invoke-Bootstrap -Fixture $fixture
        if ($result.ExitCode -ne 0) {
            throw "bootstrap failed unexpectedly: $($result.Output)"
        }
        Assert-Equal -Name 'bootstrap exit code' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'bootstrap reports exact revision and manifest pin' `
            -Condition ($result.Output -match (
                'CI_RELEASE_WORKSPACE_PASS revision=' +
                [regex]::Escape($fixture.Revision.ToLowerInvariant()) +
                ' manifest_sha256=[0-9a-f]{64}'
            ))

        $source = Join-Path $fixture.Work 'source'
        $stage = Join-Path $fixture.Work 'stage'
        $artifact = Join-Path $fixture.Work 'artifact'
        Assert-Equal -Name 'source uses committed bytes' -Expected "# committed bytes`n" `
            -Actual ([IO.File]::ReadAllText((Join-Path $source 'README.md'), $script:Utf8NoBom))
        Assert-True -Name 'untracked checkout file is absent' `
            -Condition (-not (Test-Path -LiteralPath (Join-Path $source 'untracked-secret.txt')))
        Assert-True -Name 'clean source has no Git metadata' `
            -Condition (-not (Test-Path -LiteralPath (Join-Path $source '.git')))
        Assert-True -Name 'sealed stage has no Git metadata' `
            -Condition (-not (Test-Path -LiteralPath (Join-Path $stage '.git')))
        Assert-ByteEqualTrees -ExpectedRoot $source -ActualRoot $stage
        $artifactEntries = @(Get-ChildItem -LiteralPath $artifact -Force)
        Assert-Equal -Name 'artifact initially owns only manifest' -Expected 1 -Actual $artifactEntries.Count
        Assert-Equal -Name 'artifact manifest fixed name' -Expected 'public-tree-manifest.v1.json' `
            -Actual $artifactEntries[0].Name
        foreach ($externalRoot in @('cache', 'temp')) {
            Assert-True -Name "$externalRoot root exists outside stage" `
                -Condition (Test-Path -LiteralPath (Join-Path $fixture.Work $externalRoot) -PathType Container)
            Assert-True -Name "$externalRoot is not staged" `
                -Condition (-not (Test-Path -LiteralPath (Join-Path $stage $externalRoot)))
        }
        Assert-True -Name 'temporary Git archive is deleted' `
            -Condition (@(Get-ChildItem -LiteralPath $fixture.Work -Filter '*.zip' -Force).Count -eq 0)
    }

    Invoke-Case 'Git replacement refs cannot substitute the fixed commit' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'replace object'
        $originalRevision = $fixture.Revision
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository 'replacement-only.txt') -Text "replacement`n"
        $replacementRevision = Commit-All -Repository $fixture.Repository -Message 'replacement object'
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @('reset', '--hard', $originalRevision))
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @('replace', $originalRevision, $replacementRevision))
        $fixture.Revision = $originalRevision

        $result = Invoke-Bootstrap -Fixture $fixture
        if ($result.ExitCode -ne 0) {
            throw "replace-ref bootstrap failed unexpectedly: $($result.Output)"
        }
        Assert-Equal -Name 'replace-ref bootstrap exit code' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'replacement-only file is not exported' `
            -Condition (-not (Test-Path -LiteralPath (Join-Path $fixture.Work 'source/replacement-only.txt')))
        Assert-Equal -Name 'original commit README survives replacement ref' -Expected "# committed bytes`n" `
            -Actual ([IO.File]::ReadAllText(
                (Join-Path $fixture.Work 'source/README.md'),
                $script:Utf8NoBom
            ))
    }

    Invoke-Case 'hostile ambient Git environment and global attributes are ignored' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'hostile git environment'
        $attributes = Join-Path $fixture.Root 'hostile-attributes'
        $globalConfig = Join-Path $fixture.Root 'hostile-gitconfig'
        Write-Utf8NoBom -Path $attributes -Text "* export-ignore`n"
        Write-Utf8NoBom -Path $globalConfig -Text (
            "[core]`n" +
            "`tattributesFile = $($attributes.Replace([char]92, [char]47))`n"
        )
        $hostileEnvironment = [ordered]@{
            GIT_DIR = Join-Path $fixture.Root 'missing-git-dir'
            GIT_WORK_TREE = Join-Path $fixture.Root 'missing-work-tree'
            GIT_INDEX_FILE = Join-Path $fixture.Root 'missing-index'
            GIT_ALTERNATE_OBJECT_DIRECTORIES = Join-Path $fixture.Root 'missing-objects'
            GIT_CONFIG_GLOBAL = $globalConfig
            GIT_CONFIG_SYSTEM = $globalConfig
            GIT_NO_REPLACE_OBJECTS = '0'
        }
        $savedEnvironment = [ordered]@{}
        try {
            foreach ($name in $hostileEnvironment.Keys) {
                $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
                [Environment]::SetEnvironmentVariable(
                    $name,
                    [string]$hostileEnvironment[$name],
                    'Process'
                )
            }
            $result = Invoke-Bootstrap -Fixture $fixture
        } finally {
            foreach ($name in $savedEnvironment.Keys) {
                [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
            }
        }
        Assert-Equal -Name 'hostile Git environment bootstrap exit' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'hostile global export-ignore did not remove README' `
            -Condition (Test-Path -LiteralPath (Join-Path $fixture.Work 'source/README.md') -PathType Leaf)
    }

    Invoke-Case 'post-generator same-size source tamper cannot redefine the fixed commit' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'post generator tamper'
        $generatorPath = Join-Path $fixture.Repository 'scripts/New-PublicStaging.ps1'
        $generatorSource = [IO.File]::ReadAllText($generatorPath, $script:Utf8NoBom)
        $generatorSource += @'

[IO.File]::WriteAllBytes(
    (Join-Path $SourceRoot 'README.md'),
    [Text.Encoding]::UTF8.GetBytes("# tampered bytes!`n")
)
'@
        Write-Utf8NoBom -Path $generatorPath -Text $generatorSource
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'tampering generator'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode `
            -Name 'post-generator same-size source tamper' `
            -Result $result `
            -Code 'CRW_FINAL_TREE_MISMATCH'
    }

    Invoke-Case 'post-generator stage-only tamper cannot redefine the fixed commit' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'stage only tamper'
        $generatorPath = Join-Path $fixture.Repository 'scripts/New-PublicStaging.ps1'
        $generatorSource = [IO.File]::ReadAllText($generatorPath, $script:Utf8NoBom)
        $generatorSource += @'

[IO.File]::WriteAllBytes(
    (Join-Path $StageRoot 'README.md'),
    [Text.Encoding]::UTF8.GetBytes("# tampered bytes!`n")
)
'@
        Write-Utf8NoBom -Path $generatorPath -Text $generatorSource
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'stage-only tampering generator'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode `
            -Name 'post-generator stage-only tamper' `
            -Result $result `
            -Code 'CRW_FINAL_TREE_MISMATCH'
    }

    Invoke-Case 'final verifier uses authenticated bytes after its snapshot path is replaced' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'verifier snapshot replacement'
        $generatorPath = Join-Path $fixture.Repository 'scripts/New-PublicStaging.ps1'
        $generatorSource = [IO.File]::ReadAllText($generatorPath, $script:Utf8NoBom)
        $generatorSource += @'

[IO.File]::WriteAllText(
    (Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'),
    "Write-Error 'MUTABLE_VERIFIER_PATH_WAS_EXECUTED'`nexit 91`n"
)
'@
        Write-Utf8NoBom -Path $generatorPath -Text $generatorSource
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'verifier path replacement'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-Equal -Name 'authenticated verifier bytes survive path replacement' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'replacement verifier path was not executed' `
            -Condition (
                $result.Output.IndexOf(
                    'MUTABLE_VERIFIER_PATH_WAS_EXECUTED',
                    [StringComparison]::Ordinal
                ) -lt 0
            )
    }

    Invoke-Case 'child stdout and stderr are independently bounded' {
        foreach ($scenario in @(
            @{
                Name = 'stdout'
                Payload = "[Console]::Out.Write(('O' * 8388609))"
            },
            @{
                Name = 'stderr'
                Payload = "[Console]::Error.Write(('E' * 1048577))"
            }
        )) {
            $fixture = New-GitFixture `
                -Container $suiteRoot `
                -Name ("child output " + $scenario.Name)
            $generatorPath = Join-Path $fixture.Repository 'scripts/New-PublicStaging.ps1'
            $generatorSource = [IO.File]::ReadAllText($generatorPath, $script:Utf8NoBom)
            $generatorSource += "`n$($scenario.Payload)`n"
            Write-Utf8NoBom -Path $generatorPath -Text $generatorSource
            $fixture.Revision = Commit-All `
                -Repository $fixture.Repository `
                -Message ("flood child " + $scenario.Name)
            $result = Invoke-Bootstrap -Fixture $fixture
            Assert-FailsWithCode `
                -Name ("child " + $scenario.Name + " bound") `
                -Result $result `
                -Code 'CRW_CHILD_OUTPUT_TOO_LARGE'
            Assert-True `
                -Name ("child " + $scenario.Name + " payload is not replayed") `
                -Condition ($result.Output.Length -lt 4096)
        }
    }

    Invoke-Case 'revision syntax injection is rejected before Git' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'revision injection'
        $result = Invoke-Bootstrap -Fixture $fixture -Revision 'deadbeef;Write-Host injected'
        Assert-FailsWithCode -Name 'revision injection' -Result $result -Code 'CRW_REVISION_INVALID'
    }

    Invoke-Case 'nonexistent full object id is rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'missing revision'
        $result = Invoke-Bootstrap -Fixture $fixture -Revision ('0' * 40)
        Assert-FailsWithCode -Name 'missing revision' -Result $result -Code 'CRW_REVISION_NOT_COMMIT'
    }

    Invoke-Case 'blob tree and annotated tag object ids are not accepted as commits' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'noncommit objects'
        $blob = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD:README.md')
        $tree = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD^{tree}')
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            '-c', 'tag.gpgsign=false',
            'tag', '--annotate', '--message', 'fixture tag', 'fixture-tag'
        ))
        $tag = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'fixture-tag^{tag}')
        foreach ($candidate in @($blob, $tree, $tag)) {
            $work = Join-Path $fixture.Root ('work-' + $candidate.Substring(0, 8))
            $result = Invoke-Bootstrap -Fixture $fixture -Revision $candidate -WorkRoot $work
            Assert-FailsWithCode `
                -Name "noncommit object $($candidate.Substring(0, 8))" `
                -Result $result `
                -Code 'CRW_REVISION_NOT_COMMIT'
        }
    }

    Invoke-Case 'HEAD mismatch is rejected even when requested commit exists' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'head mismatch'
        $oldRevision = $fixture.Revision
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository 'second.txt') -Text "second commit`n"
        [void](Commit-All -Repository $fixture.Repository -Message 'second')
        $result = Invoke-Bootstrap -Fixture $fixture -Revision $oldRevision
        Assert-FailsWithCode -Name 'HEAD mismatch' -Result $result -Code 'CRW_HEAD_MISMATCH'
    }

    Invoke-Case 'work root nested in checkout is rejected without creation' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'nested work'
        $nestedWork = Join-Path $fixture.Repository 'release-work'
        $result = Invoke-Bootstrap -Fixture $fixture -WorkRoot $nestedWork
        Assert-FailsWithCode -Name 'nested work root' -Result $result -Code 'CRW_ROOTS_NOT_DISJOINT'
        Assert-True -Name 'rejected nested work root remains absent' `
            -Condition (-not (Test-Path -LiteralPath $nestedWork))
    }

    Invoke-Case 'work root containing the checkout is rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'containing work'
        $result = Invoke-Bootstrap -Fixture $fixture -WorkRoot $fixture.Root
        Assert-FailsWithCode -Name 'containing work root' -Result $result -Code 'CRW_ROOTS_NOT_DISJOINT'
    }

    Invoke-Case 'gitdir file pointing outside the checkout is rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'gitdir file'
        $gitDirectory = Join-Path $fixture.Repository '.git'
        $externalGitDirectory = Join-Path $fixture.Root 'external-git-directory'
        [IO.Directory]::Move($gitDirectory, $externalGitDirectory)
        $externalPortable = $externalGitDirectory.Replace([char]92, [char]47)
        Write-Utf8NoBom -Path $gitDirectory -Text "gitdir: $externalPortable`n"
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode `
            -Name 'external gitdir file' `
            -Result $result `
            -Code 'CRW_GIT_DIRECTORY_INVALID'
    }

    Invoke-Case 'Git object alternates are rejected from the exact metadata root' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'object alternates'
        $alternates = Join-Path $fixture.Repository '.git/objects/info/alternates'
        Write-Utf8NoBom -Path $alternates -Text (
            (Join-Path $fixture.Root 'external-objects').Replace([char]92, [char]47) + "`n"
        )
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode `
            -Name 'Git object alternates' `
            -Result $result `
            -Code 'CRW_GIT_ALTERNATES_FORBIDDEN'
    }

    Invoke-Case 'nonempty work root is rejected and preserved' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'nonempty work'
        [void][IO.Directory]::CreateDirectory($fixture.Work)
        $foreign = Join-Path $fixture.Work 'foreign.txt'
        Write-Utf8NoBom -Path $foreign -Text "preserve`n"
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'nonempty work root' -Result $result -Code 'CRW_WORK_ROOT_NOT_EMPTY'
        Assert-Equal -Name 'foreign work entry is preserved' -Expected "preserve`n" `
            -Actual ([IO.File]::ReadAllText($foreign, $script:Utf8NoBom))
    }

    Invoke-Case 'work root reparse ancestry is rejected when a junction can be created' {
        if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
            Write-Host 'SKIP junction fixture is Windows-specific'
            return
        }
        $fixture = New-GitFixture -Container $suiteRoot -Name 'junction ancestry'
        $target = Join-Path $fixture.Root 'junction-target'
        $junction = Join-Path $fixture.Root 'junction-link'
        [void][IO.Directory]::CreateDirectory($target)
        & cmd.exe /d /c "mklink /J `"$junction`" `"$target`"" *> $null
        if ($LASTEXITCODE -ne 0) {
            Write-Host 'SKIP junction fixture could not be created'
            return
        }
        try {
            $result = Invoke-Bootstrap -Fixture $fixture -WorkRoot (Join-Path $junction 'work')
            Assert-FailsWithCode `
                -Name 'junction work ancestry' `
                -Result $result `
                -Code 'CRW_WORK_ROOT_REPARSE'
        } finally {
            if (Test-Path -LiteralPath $junction) {
                [IO.Directory]::Delete($junction, $false)
            }
        }
    }

    Invoke-Case 'tracked symlink mode is rejected from the exact tree' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'symlink mode'
        $payload = Join-Path $fixture.Repository '.link-payload'
        Write-Utf8NoBom -Path $payload -Text 'README.md'
        $blob = Invoke-Git -Repository $fixture.Repository -Arguments @('hash-object', '-w', '.link-payload')
        [IO.File]::Delete($payload)
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            'update-index', '--add', '--cacheinfo', '120000', $blob, 'tracked-link'
        ))
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            '-c', 'commit.gpgsign=false',
            'commit', '--quiet', '--no-verify', '-m', 'symlink'
        ))
        $fixture.Revision = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD')
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'symlink tree entry' -Result $result -Code 'CRW_TREE_ENTRY_FORBIDDEN'
    }

    Invoke-Case 'gitlink mode is rejected even without gitmodules' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'gitlink mode'
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            'update-index', '--add', '--cacheinfo', '160000', $fixture.Revision, 'vendor/module'
        ))
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            '-c', 'commit.gpgsign=false',
            'commit', '--quiet', '--no-verify', '-m', 'gitlink'
        ))
        $fixture.Revision = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD')
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'gitlink tree entry' -Result $result -Code 'CRW_TREE_ENTRY_FORBIDDEN'
    }

    Invoke-Case 'tracked gitmodules metadata is rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'gitmodules'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository '.gitmodules') -Text "[submodule `"x`"]`n"
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'gitmodules'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'gitmodules' -Result $result -Code 'CRW_GIT_METADATA_FORBIDDEN'
    }

    Invoke-Case 'tracked decomposed Unicode path is rejected before archive extraction' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'tracked decomposed path'
        $decomposedName = 'e' + [char]0x0301 + '.txt'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository $decomposedName) -Text "non-nfc`n"
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'decomposed path'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'tracked decomposed path' -Result $result -Code 'CRW_PATH_INVALID'
    }

    Invoke-Case 'tracked case-colliding paths are rejected from the Git index' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'case collision'
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @('config', 'core.ignorecase', 'false'))
        $blob = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD:README.md')
        foreach ($path in @('Case.txt', 'case.txt')) {
            [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
                'update-index', '--add', '--cacheinfo', '100644', $blob, $path
            ))
        }
        [void](Invoke-Git -Repository $fixture.Repository -Arguments @(
            '-c', 'commit.gpgsign=false',
            'commit', '--quiet', '--no-verify', '-m', 'case collision'
        ))
        $fixture.Revision = Invoke-Git -Repository $fixture.Repository -Arguments @('rev-parse', 'HEAD')
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'tracked case collision' -Result $result -Code 'CRW_PATH_COLLISION'
    }

    Invoke-Case 'LFS attributes are rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'lfs attributes'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository '.gitattributes') `
            -Text "*.bin filter=lfs diff=lfs merge=lfs -text`n"
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'lfs attributes'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'LFS attributes' -Result $result -Code 'CRW_LFS_FORBIDDEN'
    }

    Invoke-Case 'LFS pointer payload is rejected' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'lfs pointer'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository 'asset.bin') -Text (
            "version https://git-lfs.github.com/spec/v1`n" +
            "oid sha256:$('0' * 64)`n" +
            "size 123`n"
        )
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'lfs pointer'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'LFS pointer' -Result $result -Code 'CRW_LFS_FORBIDDEN'
    }

    Invoke-Case 'export-ignore cannot hide a tracked file' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'export ignore'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository '.gitattributes') -Text "hidden.txt export-ignore`n"
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository 'hidden.txt') -Text "tracked but omitted`n"
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'export ignore'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'export-ignore' -Result $result -Code 'CRW_ARCHIVE_TREE_MISMATCH'
    }

    Invoke-Case 'export-subst cannot change committed blob bytes' {
        $fixture = New-GitFixture -Container $suiteRoot -Name 'export subst'
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository '.gitattributes') -Text "substitution.txt export-subst`n"
        Write-Utf8NoBom -Path (Join-Path $fixture.Repository 'substitution.txt') -Text '$Format:%H$'
        $fixture.Revision = Commit-All -Repository $fixture.Repository -Message 'export subst'
        $result = Invoke-Bootstrap -Fixture $fixture
        Assert-FailsWithCode -Name 'export-subst' -Result $result -Code 'CRW_ARCHIVE_BLOB_MISMATCH'
    }
} finally {
    $resolvedSuite = [IO.Path]::GetFullPath($suiteRoot)
    $resolvedParent = [IO.Path]::GetFullPath($testTempRoot).
        TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
    $cleanupComparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if (-not $resolvedSuite.StartsWith(
        $resolvedParent,
        $cleanupComparison
    )) {
        throw 'CI_RELEASE_WORKSPACE_SELFTEST_CLEANUP_BOUNDARY'
    }
    if (Test-Path -LiteralPath $resolvedSuite -PathType Container) {
        Remove-Item -LiteralPath $resolvedSuite -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'CI_RELEASE_WORKSPACE_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) failures=$($script:Failures.Count)"
    )
}

Write-Host "CI_RELEASE_WORKSPACE_SELFTEST_PASS cases=$($script:Cases) assertions=$($script:Assertions)"
