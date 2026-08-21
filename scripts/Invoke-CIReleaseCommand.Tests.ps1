[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Runner = Join-Path $PSScriptRoot 'Invoke-CIReleaseCommand.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
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
            "CI_RELEASE_COMMAND_FAIL code=$Code",
            [StringComparison]::Ordinal
        )
    )
}

function New-CommandFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $root = Join-Path $Container $Name
    $working = Join-Path $root 'working'
    $output = Join-Path $root 'output'
    $private = Join-Path $root 'private'
    foreach ($directory in @($working, $output, $private)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    return [pscustomobject]@{
        Root = $root
        Working = $working
        Output = $output
        Private = $private
    }
}

function Invoke-Runner {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [int]$TimeoutSeconds = 30,
        [int64]$MaximumStandardOutputBytes = 1048576,
        [int64]$MaximumStandardErrorBytes = 1048576,
        [hashtable]$EnvironmentOverrides = @{},
        [string]$OutputRoot = '',
        [string]$StdoutPath = '',
        [string]$StderrPath = ''
    )
    if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
        $OutputRoot = $Fixture.Output
    }
    if ([string]::IsNullOrWhiteSpace($StdoutPath)) {
        $StdoutPath = Join-Path $Fixture.Output 'stdout.log'
    }
    if ([string]::IsNullOrWhiteSpace($StderrPath)) {
        $StderrPath = Join-Path $Fixture.Output 'stderr.log'
    }
    return & $script:Runner `
        -ExecutablePath $script:PowerShell `
        -ArgumentList $Arguments `
        -WorkingDirectory $Fixture.Working `
        -OutputRoot $OutputRoot `
        -StandardOutputPath $StdoutPath `
        -StandardErrorPath $StderrPath `
        -PrivateTempRoot $Fixture.Private `
        -TimeoutSeconds $TimeoutSeconds `
        -MaximumStandardOutputBytes $MaximumStandardOutputBytes `
        -MaximumStandardErrorBytes $MaximumStandardErrorBytes `
        -EnvironmentOverrides $EnvironmentOverrides
}

if (-not (Test-Path -LiteralPath $script:Runner -PathType Leaf)) {
    throw 'CI_RELEASE_COMMAND_SELFTEST_DEPENDENCY_MISSING'
}
$testTempRoot = if (-not [string]::IsNullOrWhiteSpace(
    $env:FREEAGENT_CI_COMMAND_TEST_TEMP_ROOT
)) {
    [IO.Path]::GetFullPath($env:FREEAGENT_CI_COMMAND_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}
if (-not (Test-Path -LiteralPath $testTempRoot -PathType Container)) {
    throw 'FREEAGENT_CI_COMMAND_TEST_TEMP_ROOT must be an existing directory.'
}
$suiteRoot = Join-Path $testTempRoot (
    'freeagent-ci-release-command-tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    Invoke-Case 'runner is ASCII and parses in Windows PowerShell' {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Runner,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parse error count' -Expected 0 -Actual @($errors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Runner)
        Assert-Equal -Name 'non-ASCII byte count' -Expected 0 -Actual (
            @($bytes | Where-Object { $_ -gt 127 }).Count
        )
    }

    Invoke-Case 'stdout stderr and nonzero exit remain independent' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'streams'
        $result = Invoke-Runner `
            -Fixture $fixture `
            -Arguments @(
                '-NoLogo', '-NoProfile', '-NonInteractive', '-Command',
                "[Console]::Out.Write('OUT');[Console]::Error.Write('ERR');exit 7"
            )
        Assert-Equal -Name 'exit code' -Expected 7 -Actual $result.ExitCode
        Assert-Equal -Name 'stdout bytes' -Expected 3L `
            -Actual $result.StandardOutputBytes
        Assert-Equal -Name 'stderr bytes' -Expected 3L `
            -Actual $result.StandardErrorBytes
        Assert-Equal -Name 'stdout content' -Expected 'OUT' -Actual (
            [IO.File]::ReadAllText((Join-Path $fixture.Output 'stdout.log'))
        )
        Assert-Equal -Name 'stderr content' -Expected 'ERR' -Actual (
            [IO.File]::ReadAllText((Join-Path $fixture.Output 'stderr.log'))
        )
    }

    Invoke-Case 'runner can be invoked repeatedly in one trusted process' {
        $first = New-CommandFixture -Container $suiteRoot -Name 'repeat-one'
        $second = New-CommandFixture -Container $suiteRoot -Name 'repeat-two'
        foreach ($fixture in @($first, $second)) {
            $result = Invoke-Runner `
                -Fixture $fixture `
                -Arguments @(
                    '-NoLogo', '-NoProfile', '-NonInteractive',
                    '-Command', "Write-Output 'ok';exit 0"
                )
            Assert-Equal -Name "repeat exit $($fixture.Root)" `
                -Expected 0 `
                -Actual $result.ExitCode
        }
    }

    Invoke-Case 'machine capture disables terminal control output by default' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'plain-output'
        $result = Invoke-Runner `
            -Fixture $fixture `
            -Arguments @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-Command',
                "[Console]::Out.Write([Environment]::GetEnvironmentVariable('NO_COLOR', 'Process'));exit 0"
            )
        Assert-Equal -Name 'plain output exit' -Expected 0 -Actual $result.ExitCode
        Assert-Equal -Name 'plain output bytes' -Expected 1L `
            -Actual $result.StandardOutputBytes
        Assert-Equal -Name 'plain output content' -Expected '1' -Actual (
            [IO.File]::ReadAllText((Join-Path $fixture.Output 'stdout.log'))
        )
    }

    Invoke-Case 'nested stdin command exits without terminal control output' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'stdin-eof'
        $result = Invoke-Runner `
            -Fixture $fixture `
            -Arguments @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-Command', '-'
            )
        Assert-Equal -Name 'stdin EOF exit' -Expected 0 -Actual $result.ExitCode
        Assert-Equal -Name 'stdin EOF stdout bytes' -Expected 0L `
            -Actual $result.StandardOutputBytes
        Assert-Equal -Name 'stdin EOF stderr bytes' -Expected 0L `
            -Actual $result.StandardErrorBytes
        Assert-Equal -Name 'stdin EOF stdout file' -Expected 0L -Actual (
            (Get-Item -LiteralPath (Join-Path $fixture.Output 'stdout.log')).Length
        )
        Assert-Equal -Name 'stdin EOF stderr file' -Expected 0L -Actual (
            (Get-Item -LiteralPath (Join-Path $fixture.Output 'stderr.log')).Length
        )
    }

    Invoke-Case 'GitHub command channels and credential-like values are scrubbed' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'environment'
        $names = @(
            'GITHUB_ENV',
            'ACTIONS_RUNTIME_TOKEN',
            'RUNNER_NAME',
            'CI',
            'FREEAGENT_API_KEY_SENTINEL',
            'FREEAGENT_SAFE_SENTINEL'
        )
        $saved = [ordered]@{}
        try {
            foreach ($name in $names) {
                $saved[$name] = [Environment]::GetEnvironmentVariable(
                    $name,
                    'Process'
                )
                [Environment]::SetEnvironmentVariable(
                    $name,
                    $(if ($name -ceq 'FREEAGENT_SAFE_SENTINEL') {
                        'keep'
                    } else {
                        'remove'
                    }),
                    'Process'
                )
            }
            $probe = @'
$names = @(
    'GITHUB_ENV',
    'ACTIONS_RUNTIME_TOKEN',
    'RUNNER_NAME',
    'CI',
    'FREEAGENT_API_KEY_SENTINEL',
    'FREEAGENT_SAFE_SENTINEL'
)
foreach ($name in $names) {
    [Console]::Out.WriteLine(
        [string]::Concat(
            $name,
            '=',
            [Environment]::GetEnvironmentVariable($name, 'Process')
        )
    )
}
'@
            $result = Invoke-Runner `
                -Fixture $fixture `
                -Arguments @(
                    '-NoLogo', '-NoProfile', '-NonInteractive',
                    '-Command', $probe
                )
        } finally {
            foreach ($name in $names) {
                [Environment]::SetEnvironmentVariable(
                    $name,
                    $saved[$name],
                    'Process'
                )
            }
        }
        Assert-Equal -Name 'environment probe exit' -Expected 0 -Actual $result.ExitCode
        $values = [ordered]@{}
        foreach ($line in [IO.File]::ReadAllLines(
            (Join-Path $fixture.Output 'stdout.log')
        )) {
            $index = $line.IndexOf('=')
            $values[$line.Substring(0, $index)] = $line.Substring($index + 1)
        }
        foreach ($name in $names[0..4]) {
            Assert-Equal -Name "$name absent" -Expected '' -Actual $values[$name]
        }
        Assert-Equal -Name 'safe sentinel retained' -Expected 'keep' `
            -Actual $values['FREEAGENT_SAFE_SENTINEL']
    }

    Invoke-Case 'timeout terminates the command' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'timeout'
        $clock = [Diagnostics.Stopwatch]::StartNew()
        Assert-ThrowsCode `
            -Name 'timeout' `
            -Code 'CIC_TIMEOUT' `
            -Body {
                [void](Invoke-Runner `
                    -Fixture $fixture `
                    -TimeoutSeconds 1 `
                    -Arguments @(
                        '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-Command', 'Start-Sleep -Seconds 30'
                    ))
            }
        Assert-True -Name 'timeout wall clock bounded' -Condition (
            $clock.ElapsedMilliseconds -lt 10000
        )
    }

    Invoke-Case 'output bounds fail closed without unbounded files' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'output-bound'
        Assert-ThrowsCode `
            -Name 'stdout bound' `
            -Code 'CIC_OUTPUT_TOO_LARGE' `
            -Body {
                [void](Invoke-Runner `
                    -Fixture $fixture `
                    -MaximumStandardOutputBytes 64 `
                    -Arguments @(
                        '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-Command', "[Console]::Out.Write(('X' * 65))"
                    ))
            }
        Assert-True -Name 'captured stdout remains bounded' -Condition (
            (Get-Item -LiteralPath (Join-Path $fixture.Output 'stdout.log')).
                Length -le 64
        )
    }

    Invoke-Case 'output paths and environment overrides cannot escape policy' {
        $fixture = New-CommandFixture -Container $suiteRoot -Name 'policy'
        $outside = Join-Path $fixture.Root 'outside.log'
        Assert-ThrowsCode `
            -Name 'outside output' `
            -Code 'CIC_LAYOUT_INVALID' `
            -Body {
                [void](Invoke-Runner `
                    -Fixture $fixture `
                    -StdoutPath $outside `
                    -Arguments @(
                        '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-Command', 'exit 0'
                    ))
            }
        Assert-ThrowsCode `
            -Name 'sensitive environment override' `
            -Code 'CIC_ENVIRONMENT_OVERRIDE_INVALID' `
            -Body {
                [void](Invoke-Runner `
                    -Fixture $fixture `
                    -EnvironmentOverrides @{
                        FREEAGENT_API_KEY_SENTINEL = 'not-a-secret'
                    } `
                    -Arguments @(
                        '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-Command', 'exit 0'
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
        throw 'CI_RELEASE_COMMAND_SELFTEST_CLEANUP_BOUNDARY'
    }
    if (Test-Path -LiteralPath $resolvedSuite -PathType Container) {
        Remove-Item -LiteralPath $resolvedSuite -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'CI_RELEASE_COMMAND_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'CI_RELEASE_COMMAND_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
