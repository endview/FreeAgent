[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$TempRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = [IO.Path]::GetFullPath($Root)
$externalRoot = [IO.Path]::GetFullPath($TempRoot)
if (-not [IO.Path]::IsPathRooted($Root) -or -not (Test-Path -LiteralPath $repositoryRoot -PathType Container)) {
    throw 'Test-ControlWeb.Tests Root must be an existing absolute directory.'
}
if (-not [IO.Path]::IsPathRooted($TempRoot) -or -not (Test-Path -LiteralPath $externalRoot -PathType Container)) {
    throw 'Test-ControlWeb.Tests TempRoot must be an existing absolute directory.'
}
$comparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
$prefix = $repositoryRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
if ($externalRoot.StartsWith($prefix, $comparison)) { throw 'Test-ControlWeb.Tests TempRoot must be outside Root.' }

$gate = Join-Path $repositoryRoot 'scripts/Test-ControlWeb.ps1'
$workspace = Join-Path $externalRoot ("controlweb-tests-" + [Guid]::NewGuid().ToString('N'))
$fixtureRoot = Join-Path $workspace 'fixture'
$gateTemp = Join-Path $workspace 'gate-temp'
$tools = Join-Path $workspace 'tools'
$utf8NoBom = New-Object Text.UTF8Encoding($false)

function Write-Utf8([string]$Path, [string]$Text) {
    [IO.File]::WriteAllText($Path, $Text, $utf8NoBom)
}

function Assert-Empty([string]$Path, [string]$Label) {
    if (@(Get-ChildItem -LiteralPath $Path -Force).Count -ne 0) {
        throw "$Label left external work material"
    }
}

function Expect-Failure([scriptblock]$Action, [string]$Code) {
    $message = ''
    try {
        & $Action
    } catch {
        $message = $_.Exception.Message
    }
    if ($message -notmatch [regex]::Escape("[$Code]")) {
        throw "expected [$Code], got '$message'"
    }
}

try {
    New-Item -ItemType Directory -Path $fixtureRoot, $gateTemp, $tools | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $fixtureRoot 'internal'), (Join-Path $fixtureRoot 'testdata/release') -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repositoryRoot 'internal/controlweb') -Destination (Join-Path $fixtureRoot 'internal') -Recurse
    Copy-Item -LiteralPath (Join-Path $repositoryRoot 'testdata/release/frontend-dependency-licenses.v1.json') -Destination (Join-Path $fixtureRoot 'testdata/release/frontend-dependency-licenses.v1.json')

    $fakeNode = Join-Path $tools 'node.ps1'
    $fakeNpm = Join-Path $tools 'npm.ps1'
    Write-Utf8 $fakeNode @'
if ($args.Count -eq 1 -and $args[0] -ceq '--version') { Write-Output 'v24.19.0'; exit 0 }
if ($args.Count -eq 3 -and $args[0] -like '*validate-license-manifest.mjs') { Write-Output 'PASS fake manifest validation'; exit 0 }
Write-Error 'unexpected fake Node invocation'
exit 9
'@
    Write-Utf8 $fakeNpm @'
if ($args.Count -eq 1 -and $args[0] -ceq '--version') {
    if ([string]::IsNullOrEmpty($env:CONTROLWEB_FAKE_NPM_VERSION)) { Write-Output '12.0.2' } else { Write-Output $env:CONTROLWEB_FAKE_NPM_VERSION }
    exit 0
}
if ($args.Count -ge 1 -and $args[0] -ceq 'ci') {
    if ($env:CONTROLWEB_FAKE_FAIL -ceq 'ci') { exit 7 }
    exit 0
}
if ($args.Count -eq 2 -and $args[0] -ceq 'run' -and $args[1] -ceq 'typecheck') { exit 0 }
if ($args.Count -eq 1 -and $args[0] -ceq 'test') { exit 0 }
if ($args.Count -eq 2 -and $args[0] -ceq 'run' -and $args[1] -ceq 'build') {
    Copy-Item -LiteralPath $env:CONTROLWEB_FIXTURE_DIST -Destination (Join-Path (Get-Location) 'dist') -Recurse
    $marker = Join-Path $env:TEMP 'controlweb-fake-build.marker'
    if (Test-Path -LiteralPath $marker) {
        if ($env:CONTROLWEB_FAKE_DRIFT_SECOND -ceq '1') {
            [IO.File]::AppendAllText(
                (Join-Path (Get-Location) 'dist/assets/app.css'),
                "`n",
                (New-Object Text.UTF8Encoding($false))
            )
        }
    } else {
        [IO.File]::WriteAllText(
            $marker,
            'first',
            (New-Object Text.UTF8Encoding($false))
        )
    }
    exit 0
}
Write-Error 'unexpected fake npm invocation'
exit 9
'@

    $env:CONTROLWEB_FIXTURE_DIST = Join-Path $repositoryRoot 'internal/controlweb/dist'
    $env:CONTROLWEB_FAKE_FAIL = ''
    & $gate -Root $fixtureRoot -TempRoot $gateTemp -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    Assert-Empty $gateTemp 'successful gate'

    $driftPath = Join-Path $fixtureRoot 'internal/controlweb/dist/assets/app.css'
    $original = [IO.File]::ReadAllText($driftPath, $utf8NoBom)
    Write-Utf8 $driftPath ($original + "`n")
    Expect-Failure {
        & $gate -Root $fixtureRoot -TempRoot $gateTemp -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    } 'CONTROLWEB_DIST_BYTE_MISMATCH'
    Assert-Empty $gateTemp 'drift gate'
    Write-Utf8 $driftPath $original

    $env:CONTROLWEB_FAKE_DRIFT_SECOND = '1'
    Expect-Failure {
        & $gate -Root $fixtureRoot -TempRoot $gateTemp -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    } 'CONTROLWEB_BUILD_NONDETERMINISTIC'
    Assert-Empty $gateTemp 'nondeterministic build gate'
    $env:CONTROLWEB_FAKE_DRIFT_SECOND = ''

    $env:CONTROLWEB_FAKE_FAIL = 'ci'
    Expect-Failure {
        & $gate -Root $fixtureRoot -TempRoot $gateTemp -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    } 'CONTROLWEB_NPM_CI_FAILED'
    Assert-Empty $gateTemp 'failed npm gate'
    $env:CONTROLWEB_FAKE_FAIL = ''

    $env:CONTROLWEB_FAKE_NPM_VERSION = '11.17.0'
    Expect-Failure {
        & $gate -Root $fixtureRoot -TempRoot $gateTemp -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    } 'CONTROLWEB_NPM_VERSION_MISMATCH'
    Assert-Empty $gateTemp 'npm-version gate'
    $env:CONTROLWEB_FAKE_NPM_VERSION = ''

    Expect-Failure {
        & $gate -Root $fixtureRoot -TempRoot $fixtureRoot -NodeCommand $fakeNode -NpmCommand $fakeNpm | Out-Null
    } 'CONTROLWEB_TEMP_ROOT_OVERLAP'

    $global:LASTEXITCODE = 0
    Write-Output 'PASS Test-ControlWeb.Tests: success, deterministic double build, byte drift, failure cleanup, and overlap rejection'
} finally {
    $env:CONTROLWEB_FIXTURE_DIST = $null
    $env:CONTROLWEB_FAKE_FAIL = $null
    $env:CONTROLWEB_FAKE_NPM_VERSION = $null
    $env:CONTROLWEB_FAKE_DRIFT_SECOND = $null
    if (Test-Path -LiteralPath $workspace) {
        $resolved = [IO.Path]::GetFullPath($workspace)
        $externalPrefix = $externalRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
        if (-not $resolved.StartsWith($externalPrefix, $comparison)) { throw 'self-test cleanup escaped TempRoot' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
