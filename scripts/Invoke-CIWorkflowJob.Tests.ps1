[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Job = Join-Path $PSScriptRoot 'Invoke-CIWorkflowJob.ps1'
$script:Runner = Join-Path $PSScriptRoot 'Invoke-CIReleaseCommand.ps1'
$script:PublicTreeGate = Join-Path $PSScriptRoot 'Test-PublicTree.ps1'
$script:LicenseGate = Join-Path $PSScriptRoot 'Test-License.ps1'
$script:DocsGate = Join-Path $PSScriptRoot 'Test-Docs.ps1'
$script:BrandingGate = Join-Path $PSScriptRoot 'Test-Branding.ps1'
$script:ControlWebGate = Join-Path $PSScriptRoot 'Test-ControlWeb.ps1'
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:ReleaseRevision = '0123456789abcdef0123456789abcdef01234567'
$script:CrossBuildPayload = [ordered]@{
    'VERSION' = "v0.1.0-dev.1`n"
    'LICENSE' = "fixture project license`n"
    'THIRD_PARTY_NOTICES.md' = "fixture third-party notices`n"
    'docs/INSTALL.md' = "# Install`n"
    'docs/QUICKSTART.md' = "# Quickstart`n"
    'docs/KNOWN_LIMITATIONS.md' = "# Known limitations`n"
    'docs/RELEASE_NOTES_v0.1.0-dev.1.md' = "# v0.1.0-dev.1`n"
    'docs/CHECKSUMS.md' = "# Verify checksums`n"
    'docs/PACKAGE_CONFIG.md' = "# Package config`n"
    'docs/PACKAGE_DATA.md' = "# Package data`n"
    'examples/current-v1.bootstrap.seed.json' = "{}`n"
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE' = "context license`n"
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml' = "kind: context`n"
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md' = "# Context bootstrap`n"
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json' = "{}`n"
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/LICENSE' = "echo license`n"
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.yaml' = "kind: model`n"
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md' = "# Echo bootstrap`n"
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/implementation/adapter.json' = "{}`n"
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/schemas/config.schema.json' = "{}`n"
}
$script:CrossBuildArtifactMap = [ordered]@{
    'VERSION' = 'VERSION'
    'LICENSE' = 'LICENSE'
    'THIRD_PARTY_NOTICES.md' = 'THIRD_PARTY_NOTICES.md'
    'docs/INSTALL.md' = 'INSTALL.md'
    'docs/QUICKSTART.md' = 'QUICKSTART.md'
    'docs/KNOWN_LIMITATIONS.md' = 'KNOWN_LIMITATIONS.md'
    'docs/RELEASE_NOTES_v0.1.0-dev.1.md' = 'RELEASE_NOTES.md'
    'docs/CHECKSUMS.md' = 'VERIFY_CHECKSUMS.md'
    'docs/PACKAGE_CONFIG.md' = 'config/README.md'
    'docs/PACKAGE_DATA.md' = 'data/README.md'
    'examples/current-v1.bootstrap.seed.json' = 'config/current-v1.bootstrap.seed.json'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/LICENSE' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/LICENSE'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.yaml' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.yaml'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/implementation/adapter.json' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/implementation/adapter.json'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/schemas/config.schema.json' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/schemas/config.schema.json'
}
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:Assertions++
    if (-not $Condition) { throw "ASSERT_FAIL $Name" }
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

function Assert-ThrowsCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $message = $null
    try { & $Body } catch { $message = $_.Exception.Message }
    Assert-True -Name "$Name throws" -Condition (
        -not [string]::IsNullOrWhiteSpace($message)
    )
    Assert-True -Name "$Name exact code" -Condition (
        $message.StartsWith(
            "CI_WORKFLOW_JOB_FAIL code=$Code",
            [StringComparison]::Ordinal
        )
    )
}

function New-WorkflowFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $root = Join-Path $Container $Name
    $stage = Join-Path $root 'stage'
    $artifact = Join-Path $root 'artifact'
    $execution = Join-Path $root 'execution'
    foreach ($directory in @($stage, $artifact, $execution)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    return [pscustomobject]@{
        Root = $root
        Stage = $stage
        Artifact = $artifact
        Execution = $execution
    }
}

function Initialize-CrossBuildStage {
    param([Parameter(Mandatory = $true)][string]$Stage)
    foreach ($relative in $script:CrossBuildPayload.Keys) {
        $path = Join-Path $Stage (
            $relative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )
        $parent = [IO.Path]::GetDirectoryName($path)
        [void][IO.Directory]::CreateDirectory($parent)
        [IO.File]::WriteAllText(
            $path,
            [string]$script:CrossBuildPayload[$relative],
            $script:Utf8NoBom
        )
    }
}

function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).
        Hash.ToLowerInvariant()
}

foreach ($dependency in @(
    $script:Job,
    $script:Runner,
    $script:PublicTreeGate,
    $script:LicenseGate,
    $script:DocsGate,
    $script:BrandingGate,
    $script:ControlWebGate
)) {
    if (-not (Test-Path -LiteralPath $dependency -PathType Leaf)) {
        throw "CI_WORKFLOW_JOB_SELFTEST_DEPENDENCY_MISSING path=$dependency"
    }
}
$testTempRoot = if (-not [string]::IsNullOrWhiteSpace(
    $env:FREEAGENT_CI_WORKFLOW_JOB_TEST_TEMP_ROOT
)) {
    [IO.Path]::GetFullPath($env:FREEAGENT_CI_WORKFLOW_JOB_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}
if (-not (Test-Path -LiteralPath $testTempRoot -PathType Container)) {
    throw 'FREEAGENT_CI_WORKFLOW_JOB_TEST_TEMP_ROOT must be an existing directory.'
}
$suiteRoot = Join-Path $testTempRoot (
    'freeagent-ci-workflow-job-tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
Invoke-Case 'workflow job is ASCII and parses in Windows PowerShell' {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Job,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parse error count' -Expected 0 -Actual @($errors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Job)
        Assert-Equal -Name 'non-ASCII byte count' -Expected 0 -Actual (
            @($bytes | Where-Object { $_ -gt 127 }).Count
        )
}

Invoke-Case 'workflow job kind requires exact canonical text' {
    Assert-ThrowsCode `
        -Name 'case-variant job kind' `
        -Code 'CIW_JOB_KIND_INVALID' `
        -Body {
            & $script:Job `
                -Initialize `
                -JobKind WINDOWS `
                -StageRoot $testTempRoot `
                -ArtifactRoot $testTempRoot `
                -ExecutionTempRoot $testTempRoot 6>$null
        }
}

Invoke-Case 'permanent gate pins match the current trusted script bytes' {
    $source = [IO.File]::ReadAllText($script:Job, $script:Utf8NoBom)
    $pins = [ordered]@{
        PublicTreeGateSha256 = $script:PublicTreeGate
        LicenseGateSha256 = $script:LicenseGate
        DocsGateSha256 = $script:DocsGate
        BrandingGateSha256 = $script:BrandingGate
        ControlWebGateSha256 = $script:ControlWebGate
    }
    foreach ($name in $pins.Keys) {
        $pattern = '(?m)^\$script:' + [Regex]::Escape($name) +
            "\s*=\s*'([0-9a-f]{64})'"
        $matches = [Regex]::Matches($source, $pattern)
        Assert-Equal -Name "$name assignment count" `
            -Expected 1 `
            -Actual $matches.Count
        Assert-Equal -Name "$name current bytes" `
            -Expected (Get-LowerSha256 -Path $pins[$name]) `
            -Actual $matches[0].Groups[1].Value
    }
}

Invoke-Case 'permanent job requires the external exact npm tool and control-web gate' {
    $source = [IO.File]::ReadAllText($script:Job, $script:Utf8NoBom)
    foreach ($required in @(
        "`$env:FREEAGENT_NPM_CLI",
        "-Code 'CIW_NPM_CLI_MISSING'",
        "-Code 'CIW_NPM_CLI_INVALID'",
        "-Code 'CIW_CONTROLWEB_GATE_AUTH_FAILED'",
        "Label = 'CONTROLWEB'",
        "'-NpmCommand', `$npmCLI",
        "'-NodeCommand', `$node",
        "'-TempRoot', `$controlWebTemp"
    )) {
        Assert-True -Name "workflow job contains $required" -Condition (
            $source.IndexOf($required, [StringComparison]::Ordinal) -ge 0
        )
    }
}

Invoke-Case 'linux race proof binds the executed compiler to go env CC' {
    $source = [IO.File]::ReadAllText($script:Job, $script:Utf8NoBom)
    Assert-True -Name 'go env captures compiler' -Condition (
        $source.IndexOf(
            "-Arguments @('env', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'CC')",
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'resolved compiler is injected as CC' -Condition (
        $source.IndexOf(
            '$goEnvironment.CC = $gcc',
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'compiler proof must equal injected CC' -Condition (
        $source.IndexOf(
            '$goProof.CC -cne $gcc',
            [StringComparison]::Ordinal
        ) -ge 0 -and $source.IndexOf(
            '$goProof.CC -ceq $gcc',
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'evidence records proved CC' -Condition (
        $source.IndexOf(
            'CC = $goProof.CC',
            [StringComparison]::Ordinal
        ) -ge 0
    )
}

Invoke-Case 'linux race command contract uses the extended timeout' {
    $source = [IO.File]::ReadAllText($script:Job, $script:Utf8NoBom)
    Assert-True -Name 'race evidence contract uses 60m' -Condition (
        $source.IndexOf(
            "Command = 'go test -json -race -count=1 -timeout=60m ./...'",
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'race executed argv uses 60m' -Condition (
        $source.IndexOf(
            "'test', '-json', '-race', '-count=1', '-timeout=60m', './...'",
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'race runner allows ten-minute margin' -Condition (
        $source.IndexOf(
            '$runnerTimeout = 4200',
            [StringComparison]::Ordinal
        ) -ge 0
    )
}

Invoke-Case 'completed evidence records a flat executed argv' {
    $source = [IO.File]::ReadAllText($script:Job, $script:Utf8NoBom)
    Assert-True -Name 'executed argv concatenates scalar and argument arrays' `
        -Condition (
            $source.IndexOf(
                '$evidence.command_argv = @($go) + @($testArguments)',
                [StringComparison]::Ordinal
            ) -ge 0
        )
    Assert-True -Name 'nested executed argv form is absent' -Condition (
        $source.IndexOf(
            '$evidence.command_argv = @($go, $testArguments)',
            [StringComparison]::Ordinal
        ) -lt 0
    )
}

Invoke-Case 'ordinary evidence initializes an exact raw stream contract' {
        foreach ($scenario in @(
            @{
                Kind = 'linux-quality'
                Relative = 'evidence/linux-ordinary'
            },
            @{
                Kind = 'windows'
                Relative = 'evidence/windows-ordinary'
            }
        )) {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name $scenario.Kind
            & $script:Job `
                -Initialize `
                -JobKind $scenario.Kind `
                -StageRoot $fixture.Stage `
                -ArtifactRoot $fixture.Artifact `
                -ExecutionTempRoot $fixture.Execution 6>$null
            $directory = Join-Path $fixture.Artifact $scenario.Relative
            [string[]]$names = @(Get-ChildItem -LiteralPath $directory -Force |
                ForEach-Object { $_.Name })
            [Array]::Sort($names, [StringComparer]::Ordinal)
            Assert-Equal -Name "$($scenario.Kind) exact files" `
                -Expected (
                    "evidence.json`nexit-code.txt`n" +
                    "go-test.stderr.log`ngo-test.stdout.jsonl"
                ) `
                -Actual ($names -join "`n")
            $evidence = [IO.File]::ReadAllText(
                (Join-Path $directory 'evidence.json')
            ) | ConvertFrom-Json
            Assert-Equal -Name "$($scenario.Kind) schema" `
                -Expected 2 `
                -Actual $evidence.schema_version
            Assert-Equal -Name "$($scenario.Kind) initial status" `
                -Expected 'UNPROVED' `
                -Actual $evidence.status
            Assert-Equal -Name "$($scenario.Kind) stdout field" `
                -Expected 'go-test.stdout.jsonl' `
                -Actual $evidence.go_test_stdout
            Assert-Equal -Name "$($scenario.Kind) stderr field" `
                -Expected 'go-test.stderr.log' `
                -Actual $evidence.go_test_stderr
        }
    }

    Invoke-Case 'race evidence initializes packages and separate raw streams' {
        foreach ($scenario in @(
            @{
                Kind = 'linux-race'
                Relative = 'evidence/linux-race'
                Packages = "./...`n"
            }
        )) {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name $scenario.Kind
            & $script:Job `
                -Initialize `
                -JobKind $scenario.Kind `
                -StageRoot $fixture.Stage `
                -ArtifactRoot $fixture.Artifact `
                -ExecutionTempRoot $fixture.Execution 6>$null
            $directory = Join-Path $fixture.Artifact $scenario.Relative
            [string[]]$names = @(Get-ChildItem -LiteralPath $directory -Force |
                ForEach-Object { $_.Name })
            [Array]::Sort($names, [StringComparer]::Ordinal)
            Assert-Equal -Name "$($scenario.Kind) exact files" `
                -Expected (
                    "evidence.json`nexit-code.txt`n" +
                    "go-test.stderr.log`ngo-test.stdout.jsonl`npackages.txt"
                ) `
                -Actual ($names -join "`n")
            Assert-Equal -Name "$($scenario.Kind) packages init" `
                -Expected $scenario.Packages `
                -Actual ([IO.File]::ReadAllText(
                    (Join-Path $directory 'packages.txt')
                ))
        }
    }

    Invoke-Case 'jobs without evidence do not create artifact entries' {
        foreach ($kind in @('permanent', 'cross-build')) {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name "empty-$kind"
            & $script:Job `
                -Initialize `
                -JobKind $kind `
                -StageRoot $fixture.Stage `
                -ArtifactRoot $fixture.Artifact `
                -ExecutionTempRoot $fixture.Execution 6>$null
            Assert-Equal -Name "$kind artifact remains empty" -Expected 0 -Actual (
                @(Get-ChildItem -LiteralPath $fixture.Artifact -Force).Count
            )
        }
    }

    Invoke-Case 'command runner pin fails before tool discovery' {
        $fixture = New-WorkflowFixture -Container $suiteRoot -Name 'bad-pin'
        Assert-ThrowsCode `
            -Name 'runner pin mismatch' `
            -Code 'CIW_COMMAND_RUNNER_AUTH_FAILED' `
            -Body {
                & $script:Job `
                    -Run `
                    -JobKind cross-build `
                    -StageRoot $fixture.Stage `
                    -ArtifactRoot $fixture.Artifact `
                    -ExecutionTempRoot $fixture.Execution `
                    -CommandRunnerPath $script:Runner `
                    -ExpectedCommandRunnerSha256 ('0' * 64) `
                    -Revision $script:ReleaseRevision `
                    -TargetGoos linux `
                    -TargetGoarch amd64 6>$null
            }
    }

    Invoke-Case 'cross build requires one lowercase 40-hex revision' {
        foreach ($invalid in @('', ('a' * 39), ('a' * 64), ('A' * 40))) {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name ('invalid-revision-' + [Guid]::NewGuid().ToString('N'))
            Assert-ThrowsCode `
                -Name "invalid cross revision length=$($invalid.Length)" `
                -Code 'CIW_RELEASE_REVISION_INVALID' `
                -Body {
                    & $script:Job `
                        -Run `
                        -JobKind cross-build `
                        -StageRoot $fixture.Stage `
                        -ArtifactRoot $fixture.Artifact `
                        -ExecutionTempRoot $fixture.Execution `
                        -CommandRunnerPath $script:Runner `
                        -ExpectedCommandRunnerSha256 (
                            Get-LowerSha256 -Path $script:Runner
                        ) `
                        -Revision $invalid `
                        -TargetGoos linux `
                        -TargetGoarch amd64 6>$null
                }
        }
    }

    Invoke-Case 'non-cross jobs reject release revision injection' {
        $fixture = New-WorkflowFixture `
            -Container $suiteRoot `
            -Name 'permanent-with-revision'
        Assert-ThrowsCode `
            -Name 'permanent release revision' `
            -Code 'CIW_RELEASE_REVISION_INVALID' `
            -Body {
                & $script:Job `
                    -Run `
                    -JobKind permanent `
                    -StageRoot $fixture.Stage `
                    -ArtifactRoot $fixture.Artifact `
                    -ExecutionTempRoot $fixture.Execution `
                    -CommandRunnerPath $script:Runner `
                    -ExpectedCommandRunnerSha256 (
                        Get-LowerSha256 -Path $script:Runner
                    ) `
                    -Revision $script:ReleaseRevision 6>$null
            }
    }

    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        Invoke-Case 'tampered permanent gate bytes fail before gate execution' {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name 'tampered-permanent-gate'
            $stageScripts = Join-Path $fixture.Stage 'scripts'
            [void][IO.Directory]::CreateDirectory($stageScripts)
            foreach ($gate in @(
                $script:PublicTreeGate,
                $script:LicenseGate,
                $script:DocsGate,
                $script:ControlWebGate
            )) {
                [IO.File]::Copy(
                    $gate,
                    (Join-Path $stageScripts ([IO.Path]::GetFileName($gate))),
                    $false
                )
            }
            [IO.File]::AppendAllText(
                (Join-Path $stageScripts 'Test-PublicTree.ps1'),
                "`n# tamper`n",
                $script:Utf8NoBom
            )
            $tools = Join-Path $fixture.Root 'tools'
            [void][IO.Directory]::CreateDirectory($tools)
            [IO.File]::WriteAllText(
                (Join-Path $tools 'go.cmd'),
                @'
@echo off
if "%1"=="mod" exit /b 0
exit /b 9
'@,
                $script:Utf8NoBom
            )
            [IO.File]::WriteAllText(
                (Join-Path $tools 'gofmt.cmd'),
                "@echo off`r`nexit /b 0`r`n",
                $script:Utf8NoBom
            )
            $savedPath = $env:PATH
            try {
                $env:PATH = $tools + [IO.Path]::PathSeparator + $savedPath
                Assert-ThrowsCode `
                    -Name 'tampered public tree gate' `
                    -Code 'CIW_PUBLIC_TREE_GATE_AUTH_FAILED' `
                    -Body {
                        & $script:Job `
                            -Run `
                            -JobKind permanent `
                            -StageRoot $fixture.Stage `
                            -ArtifactRoot $fixture.Artifact `
                            -ExecutionTempRoot $fixture.Execution `
                            -CommandRunnerPath $script:Runner `
                            -ExpectedCommandRunnerSha256 (
                                Get-LowerSha256 -Path $script:Runner
                            ) 6>$null
                    }
            } finally {
                $env:PATH = $savedPath
            }
        }

        Invoke-Case 'empty command streams preserve the original failure code' {
            $fixture = New-WorkflowFixture `
                -Container $suiteRoot `
                -Name 'empty-failure-streams'
            $toolDirectory = Join-Path $fixture.Root 'tools'
            [void][IO.Directory]::CreateDirectory($toolDirectory)
            $fakeGo = Join-Path $toolDirectory 'go.cmd'
            [IO.File]::WriteAllText(
                $fakeGo,
                "@echo off`r`nexit /b 9`r`n",
                $script:Utf8NoBom
            )
            $savedPath = $env:PATH
            try {
                $env:PATH =
                    $toolDirectory + [IO.Path]::PathSeparator + $savedPath
                Assert-ThrowsCode `
                    -Name 'empty stream command failure' `
                    -Code 'CIW_COMMAND_FAILED_GO_MOD_DOWNLOAD' `
                    -Body {
                        & $script:Job `
                            -Run `
                            -JobKind cross-build `
                            -StageRoot $fixture.Stage `
                            -ArtifactRoot $fixture.Artifact `
                            -ExecutionTempRoot $fixture.Execution `
                            -CommandRunnerPath $script:Runner `
                            -ExpectedCommandRunnerSha256 (
                                Get-LowerSha256 -Path $script:Runner
                            ) `
                            -Revision $script:ReleaseRevision `
                            -TargetGoos linux `
                            -TargetGoarch amd64 6>$null
                    }
            } finally {
                $env:PATH = $savedPath
            }
        }

        Invoke-Case 'cross build consumes the pinned runner and external artifact root' {
            $fixture = New-WorkflowFixture -Container $suiteRoot -Name 'cross'
            Initialize-CrossBuildStage -Stage $fixture.Stage
            $toolDirectory = Join-Path $fixture.Root 'tools'
            [void][IO.Directory]::CreateDirectory($toolDirectory)
            $fakeGo = Join-Path $toolDirectory 'go.cmd'
            $buildArgsPath = Join-Path $toolDirectory 'build-args.txt'
            [IO.File]::WriteAllText(
                $fakeGo,
                @'
@echo off
if "%1"=="mod" exit /b 0
if "%1"=="build" goto build
exit /b 3
:build
shift
:record
if "%~1"=="" exit /b 4
>>"%CIW_TEST_BUILD_ARGS%" echo(%~1
if "%~1"=="-o" goto output
shift
goto record
:loop
if "%1"=="" exit /b 4
if "%1"=="-o" goto output
shift
goto loop
:output
shift
>"%~1" echo fake-binary
exit /b 0
'@,
                $script:Utf8NoBom
            )
            $savedPath = $env:PATH
            $savedBuildArgs = $env:CIW_TEST_BUILD_ARGS
            try {
                $env:CIW_TEST_BUILD_ARGS = $buildArgsPath
                $env:PATH = $toolDirectory + [IO.Path]::PathSeparator + $savedPath
                & $script:Job `
                    -Run `
                    -JobKind cross-build `
                    -StageRoot $fixture.Stage `
                    -ArtifactRoot $fixture.Artifact `
                    -ExecutionTempRoot $fixture.Execution `
                    -CommandRunnerPath $script:Runner `
                    -ExpectedCommandRunnerSha256 (
                        Get-LowerSha256 -Path $script:Runner
                    ) `
                    -Revision $script:ReleaseRevision `
                    -TargetGoos windows `
                    -TargetGoarch amd64 6>$null
            } finally {
                $env:PATH = $savedPath
                $env:CIW_TEST_BUILD_ARGS = $savedBuildArgs
            }
            $binary = Join-Path $fixture.Artifact (
                'bin/freeagent-windows-amd64.exe'
            )
            Assert-True -Name 'cross binary exists' -Condition (
                Test-Path -LiteralPath $binary -PathType Leaf
            )
            Assert-Equal -Name 'cross binary fixture bytes' `
                -Expected 'fake-binary' `
                -Actual ([IO.File]::ReadAllText($binary).Trim())
            $argumentText = [IO.File]::ReadAllText(
                $buildArgsPath
            ).Replace("`r", '')
            foreach ($requiredArgument in @(
                '-trimpath',
                '-ldflags',
                '-buildid=',
                '-X=main.buildVersion=v0.1.0-dev.1',
                "-X=main.buildCommit=$($script:ReleaseRevision)",
                '-X=main.buildTarget=windows/amd64'
            )) {
                Assert-True -Name "go build argument $requiredArgument" -Condition (
                    $argumentText.IndexOf(
                        $requiredArgument,
                        [StringComparison]::Ordinal
                    ) -ge 0
                )
            }
            foreach ($sourceRelative in $script:CrossBuildArtifactMap.Keys) {
                $artifactRelative =
                    [string]$script:CrossBuildArtifactMap[$sourceRelative]
                $sourcePath = Join-Path $fixture.Stage (
                    $sourceRelative.Replace(
                        [char]47,
                        [IO.Path]::DirectorySeparatorChar
                    )
                )
                $artifactPath = Join-Path $fixture.Artifact (
                    $artifactRelative.Replace(
                        [char]47,
                        [IO.Path]::DirectorySeparatorChar
                    )
                )
                Assert-True -Name "release payload exists $artifactRelative" `
                    -Condition (
                        Test-Path -LiteralPath $artifactPath -PathType Leaf
                    )
                Assert-Equal -Name "release payload bytes $artifactRelative" `
                    -Expected (Get-LowerSha256 -Path $sourcePath) `
                    -Actual (Get-LowerSha256 -Path $artifactPath)
            }
            Assert-Equal -Name 'release VERSION exact bytes' `
                -Expected "v0.1.0-dev.1`n" `
                -Actual ([IO.File]::ReadAllText(
                    (Join-Path $fixture.Artifact 'VERSION'),
                    $script:Utf8NoBom
                ))
        }
    }
} finally {
    $resolvedSuite = [IO.Path]::GetFullPath($suiteRoot)
    $resolvedParent = [IO.Path]::GetFullPath($testTempRoot).
        TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
    $comparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if (-not $resolvedSuite.StartsWith($resolvedParent, $comparison)) {
        throw 'CI_WORKFLOW_JOB_SELFTEST_CLEANUP_BOUNDARY'
    }
    if (Test-Path -LiteralPath $resolvedSuite -PathType Container) {
        Remove-Item -LiteralPath $resolvedSuite -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'CI_WORKFLOW_JOB_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'CI_WORKFLOW_JOB_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
