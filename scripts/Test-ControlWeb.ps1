[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$TempRoot,
    [string]$NodeCommand = 'node',
    [string]$NpmCommand = 'npm'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$expectedNodeVersion = 'v24.19.0'
$expectedNpmVersion = '12.0.2'
$expectedPackageLockSHA256 = 'ba936c51835653aa867a1af4325da6b0bec64ff6e2e1fe22292bb1a829b23d5c'
$expectedLicenseManifestSHA256 = '55c35096c30631dc86950841891c8efe3f577bdd64055a63989e904a7bd2ab03'
$expectedInputs = @(
    '.node-version',
    '.npmrc',
    'index.html',
    'package-lock.json',
    'package.json',
    'scripts/normalize-dist.mjs',
    'scripts/test.mjs',
    'scripts/validate-license-manifest.mjs',
    'scripts/validate-ui.mjs',
    'src/app.tsx',
    'src/contracts.ts',
    'src/i18n/catalogs/en-US.ts',
    'src/i18n/catalogs/zh-CN.ts',
    'src/i18n/core.ts',
    'src/i18n/index.ts',
    'src/i18n/locale-preference-store.ts',
    'src/i18n/locale-selector.tsx',
    'src/i18n/provider.tsx',
    'src/i18n/resources.ts',
    'src/main.tsx',
    'src/management-ui.tsx',
    'src/management.ts',
    'src/modules-ui.tsx',
    'src/modules.ts',
    'src/overview.ts',
    'src/session.ts',
    'src/styles.css',
    'src/ui.tsx',
    'src/upgrade-reviews-ui.tsx',
    'tests/controlweb.test.tsx',
    'tests/i18n.test.tsx',
    'tests/modules.test.tsx',
    'tsconfig.json',
    'vite.config.ts'
)
$expectedDist = @(
    'assets/app.css',
    'assets/app.js',
    'assets/react.js',
    'assets/tanstack-query.js',
    'index.html'
)

function Fail([string]$Code, [string]$Message) {
    throw "[$Code] $Message"
}

function Resolve-AbsoluteDirectory([string]$Path, [string]$Code, [string]$Label) {
    if ([string]::IsNullOrWhiteSpace($Path) -or -not [IO.Path]::IsPathRooted($Path)) {
        Fail $Code "$Label must be an absolute path"
    }
    $full = [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    if (-not (Test-Path -LiteralPath $full -PathType Container)) {
        Fail $Code "$Label must be an existing directory"
    }
    return $full
}

function Test-IsInside([string]$Parent, [string]$Candidate) {
    $comparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
    $prefix = $Parent + [IO.Path]::DirectorySeparatorChar
    return $Candidate.StartsWith($prefix, $comparison)
}

function Get-SHA256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-RelativeSlashPath([string]$Base, [string]$Path) {
    return $Path.Substring($Base.Length + 1).Replace([char]92, [char]47)
}

function Assert-NoReparseAncestors([string]$Path, [string]$Code, [string]$Label) {
    $cursor = [IO.Path]::GetFullPath($Path)
    while (-not [string]::IsNullOrEmpty($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail $Code "$Label crosses a reparse point: $cursor"
            }
        }
        $parent = [IO.Directory]::GetParent($cursor)
        if ($null -eq $parent) { break }
        $cursor = $parent.FullName
    }
}

function Resolve-Tool([string]$Command, [string]$Code, [string]$Label) {
    if ([string]::IsNullOrWhiteSpace($Command)) { Fail $Code "$Label command is blank" }
    if ([IO.Path]::IsPathRooted($Command)) {
        $full = [IO.Path]::GetFullPath($Command)
        if (-not (Test-Path -LiteralPath $full -PathType Leaf)) { Fail $Code "$Label command is missing" }
        return $full
    }
    $resolved = Get-Command $Command -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $resolved) { Fail $Code "$Label command could not be resolved" }
    return $resolved.Source
}

function Invoke-Tool([string]$Tool, [string[]]$Arguments, [string]$WorkingDirectory, [string]$Code) {
    Push-Location -LiteralPath $WorkingDirectory
    try {
        & $Tool @Arguments
        if ($LASTEXITCODE -ne 0) { Fail $Code "$Tool exited with code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
}

function Invoke-Npm([string]$Node, [string]$Npm, [string[]]$Arguments, [string]$WorkingDirectory, [string]$Code) {
    if ([IO.Path]::GetExtension($Npm) -ceq '.js') {
        Invoke-Tool $Node (@($Npm) + $Arguments) $WorkingDirectory $Code
        return
    }
    Invoke-Tool $Npm $Arguments $WorkingDirectory $Code
}

function Get-NpmVersion([string]$Node, [string]$Npm) {
    if ([IO.Path]::GetExtension($Npm) -ceq '.js') {
        return (& $Node $Npm --version 2>&1 | Out-String).Trim()
    }
    return (& $Npm --version 2>&1 | Out-String).Trim()
}

function Get-ExactFiles([string]$Base, [string[]]$Expected, [string]$Code, [string]$Label) {
    $actual = @(
        Get-ChildItem -LiteralPath $Base -File -Recurse -Force | ForEach-Object {
            $relative = Get-RelativeSlashPath $Base $_.FullName
            # Local dependencies are ignored here; the release project is rebuilt
            # from the exact allowlist below with npm ci.
            if ($relative.StartsWith('node_modules/', [StringComparison]::Ordinal)) {
                return
            }
            if (($null -ne $_.LinkType) -or (($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
                Fail $Code "$Label contains a reparse file"
            }
            $relative
        } | Sort-Object -CaseSensitive
    )
    $orderedExpected = @($Expected | Sort-Object -CaseSensitive)
    if (($actual -join "`n") -cne ($orderedExpected -join "`n")) {
        Fail $Code "$Label file set differs from the exact allowlist"
    }
    return $actual
}

function Assert-LockPolicy([string]$Project) {
    $packagePath = Join-Path $Project 'package.json'
    $lockPath = Join-Path $Project 'package-lock.json'
    if ((Get-SHA256 $lockPath) -cne $expectedPackageLockSHA256) {
        Fail 'CONTROLWEB_LOCK_HASH_MISMATCH' 'package-lock.json does not match the release-gate pin'
    }
    $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    $lockText = Get-Content -LiteralPath $lockPath -Raw -Encoding UTF8
    if ($lockText -notmatch '(?m)^  "lockfileVersion": 3,$' -or $package.packageManager -cne "npm@$expectedNpmVersion") {
        Fail 'CONTROLWEB_LOCK_POLICY_INVALID' 'npm version or package-lock schema is not exact'
    }
    $runtimeNames = @($package.dependencies.PSObject.Properties.Name | Sort-Object -CaseSensitive)
    if (($runtimeNames -join "`n") -cne (@('@tanstack/react-query', 'react', 'react-dom') -join "`n")) {
        Fail 'CONTROLWEB_RUNTIME_DEPENDENCY_INVALID' 'runtime dependencies are not the exact allowlist'
    }
    foreach ($property in @($package.dependencies.PSObject.Properties) + @($package.devDependencies.PSObject.Properties)) {
        if ([string]$property.Value -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
            Fail 'CONTROLWEB_DEPENDENCY_PIN_INVALID' "$($property.Name) is not an exact version"
        }
    }
}

$repositoryRoot = Resolve-AbsoluteDirectory $Root 'CONTROLWEB_ROOT_INVALID' 'Root'
$externalRoot = Resolve-AbsoluteDirectory $TempRoot 'CONTROLWEB_TEMP_ROOT_INVALID' 'TempRoot'
if ((Test-IsInside $repositoryRoot $externalRoot) -or (Test-IsInside $externalRoot $repositoryRoot) -or $externalRoot -eq $repositoryRoot) {
    Fail 'CONTROLWEB_TEMP_ROOT_OVERLAP' 'TempRoot must be disjoint from Root'
}
Assert-NoReparseAncestors $repositoryRoot 'CONTROLWEB_REPARSE_FORBIDDEN' 'Root'
Assert-NoReparseAncestors $externalRoot 'CONTROLWEB_REPARSE_FORBIDDEN' 'TempRoot'

$node = Resolve-Tool $NodeCommand 'CONTROLWEB_NODE_MISSING' 'Node'
$npm = Resolve-Tool $NpmCommand 'CONTROLWEB_NPM_MISSING' 'npm'
$source = Join-Path $repositoryRoot 'internal/controlweb'
if (-not (Test-Path -LiteralPath $source -PathType Container)) { Fail 'CONTROLWEB_SOURCE_MISSING' 'internal/controlweb is missing' }

$sourceInputs = Get-ExactFiles $source (@($expectedInputs) + @($expectedDist | ForEach-Object { 'dist/' + $_ }) + @('README.md', 'embed.go', 'embed_test.go')) 'CONTROLWEB_SOURCE_SET_INVALID' 'control-web source'
Assert-LockPolicy $source
$licenseManifest = Join-Path $repositoryRoot 'testdata/release/frontend-dependency-licenses.v1.json'
if (-not (Test-Path -LiteralPath $licenseManifest -PathType Leaf) -or (Get-SHA256 $licenseManifest) -cne $expectedLicenseManifestSHA256) {
    Fail 'CONTROLWEB_LICENSE_MANIFEST_INVALID' 'frontend dependency license manifest is missing or differs from its pin'
}
$before = @{}
foreach ($relative in $sourceInputs) { $before[$relative] = Get-SHA256 (Join-Path $source $relative) }

$work = Join-Path $externalRoot ("controlweb-" + [Guid]::NewGuid().ToString('N'))
$project = Join-Path $work 'project'
$cache = Join-Path $work 'npm-cache'
$temporary = Join-Path $work 'tmp'
$firstBuild = Join-Path $work 'dist-build-one'
$primaryError = $null
try {
    New-Item -ItemType Directory -Path $project, $cache, $temporary -ErrorAction Stop | Out-Null
    foreach ($relative in $expectedInputs) {
        $target = Join-Path $project $relative
        $targetParent = Split-Path -Parent $target
        if (-not (Test-Path -LiteralPath $targetParent)) { New-Item -ItemType Directory -Path $targetParent -Force | Out-Null }
        Copy-Item -LiteralPath (Join-Path $source $relative) -Destination $target
    }
    $releaseDirectory = Join-Path $project 'release'
    New-Item -ItemType Directory -Path $releaseDirectory | Out-Null
    Copy-Item -LiteralPath $licenseManifest -Destination (Join-Path $releaseDirectory 'frontend-dependency-licenses.v1.json')

    $oldCache = $env:npm_config_cache
    $oldTemp = $env:TEMP
    $oldTmp = $env:TMP
    $oldPath = $env:PATH
    try {
        $env:npm_config_cache = $cache
        $env:TEMP = $temporary
        $env:TMP = $temporary
        $env:PATH = (Split-Path -Parent $node) + [IO.Path]::PathSeparator + $oldPath

        $nodeVersion = (& $node --version 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -ne 0 -or $nodeVersion -cne $expectedNodeVersion) {
            Fail 'CONTROLWEB_NODE_VERSION_MISMATCH' "Node version is '$nodeVersion', expected '$expectedNodeVersion'"
        }
        $npmVersion = Get-NpmVersion $node $npm
        if ($LASTEXITCODE -ne 0 -or $npmVersion -cne $expectedNpmVersion) {
            Fail 'CONTROLWEB_NPM_VERSION_MISMATCH' "npm version is '$npmVersion', expected '$expectedNpmVersion'"
        }

        Invoke-Npm $node $npm @('ci', '--ignore-scripts', '--no-audit', '--no-fund') $project 'CONTROLWEB_NPM_CI_FAILED'
        Invoke-Npm $node $npm @('run', 'typecheck') $project 'CONTROLWEB_TYPECHECK_FAILED'
        Invoke-Npm $node $npm @('test') $project 'CONTROLWEB_TEST_FAILED'
        Invoke-Npm $node $npm @('run', 'build') $project 'CONTROLWEB_BUILD_FAILED'
        $firstBuiltDist = Join-Path $project 'dist'
        [void](Get-ExactFiles `
            $firstBuiltDist `
            $expectedDist `
            'CONTROLWEB_DIST_SET_MISMATCH' `
            'first generated dist')
        Copy-Item `
            -LiteralPath $firstBuiltDist `
            -Destination $firstBuild `
            -Recurse
        Remove-Item -LiteralPath $firstBuiltDist -Recurse -Force
        if (Test-Path -LiteralPath $firstBuiltDist) {
            Fail 'CONTROLWEB_BUILD_RESET_FAILED' 'first generated dist could not be removed'
        }
        Invoke-Npm $node $npm @('run', 'build') $project 'CONTROLWEB_BUILD_FAILED'
        Invoke-Tool $node @(
            'scripts/validate-license-manifest.mjs',
            'package-lock.json',
            'release/frontend-dependency-licenses.v1.json'
        ) $project 'CONTROLWEB_LICENSE_MANIFEST_FAILED'
    } finally {
        $env:npm_config_cache = $oldCache
        $env:TEMP = $oldTemp
        $env:TMP = $oldTmp
        $env:PATH = $oldPath
    }

    $builtDist = Join-Path $project 'dist'
    $actualDist = Get-ExactFiles $builtDist $expectedDist 'CONTROLWEB_DIST_SET_MISMATCH' 'generated dist'
    $firstDist = Get-ExactFiles $firstBuild $expectedDist 'CONTROLWEB_DIST_SET_MISMATCH' 'first generated dist'
    if (($firstDist -join "`n") -cne ($actualDist -join "`n")) {
        Fail 'CONTROLWEB_BUILD_NONDETERMINISTIC' 'clean builds produced different asset sets'
    }
    foreach ($relative in $actualDist) {
        $committed = Join-Path (Join-Path $source 'dist') $relative
        $firstGenerated = Join-Path $firstBuild $relative
        $generated = Join-Path $builtDist $relative
        if ((Get-SHA256 $firstGenerated) -cne (Get-SHA256 $generated) -or
            (Get-Item -LiteralPath $firstGenerated).Length -ne
                (Get-Item -LiteralPath $generated).Length) {
            Fail 'CONTROLWEB_BUILD_NONDETERMINISTIC' "clean builds differ: $relative"
        }
        if ((Get-SHA256 $committed) -cne (Get-SHA256 $generated) -or (Get-Item -LiteralPath $committed).Length -ne (Get-Item -LiteralPath $generated).Length) {
            Fail 'CONTROLWEB_DIST_BYTE_MISMATCH' "generated asset differs: $relative"
        }
    }
} catch {
    $primaryError = $_
} finally {
    if (Test-Path -LiteralPath $work) {
        $resolvedWork = [IO.Path]::GetFullPath($work)
        if (-not (Test-IsInside $externalRoot $resolvedWork)) {
            if ($null -eq $primaryError) { $primaryError = [Management.Automation.ErrorRecord]::new([InvalidOperationException]::new('[CONTROLWEB_CLEANUP_PATH_INVALID] cleanup path escaped TempRoot'), 'CONTROLWEB_CLEANUP_PATH_INVALID', [Management.Automation.ErrorCategory]::InvalidData, $resolvedWork) }
        } else {
            Remove-Item -LiteralPath $resolvedWork -Recurse -Force -ErrorAction SilentlyContinue
            if ((Test-Path -LiteralPath $resolvedWork) -and $null -eq $primaryError) {
                $primaryError = [Management.Automation.ErrorRecord]::new([IOException]::new('[CONTROLWEB_CLEANUP_FAILED] external work directory remains'), 'CONTROLWEB_CLEANUP_FAILED', [Management.Automation.ErrorCategory]::WriteError, $resolvedWork)
            }
        }
    }
}

if ($null -ne $primaryError) { throw $primaryError }
$afterSourceInputs = Get-ExactFiles $source (@($expectedInputs) + @($expectedDist | ForEach-Object { 'dist/' + $_ }) + @('README.md', 'embed.go', 'embed_test.go')) 'CONTROLWEB_SOURCE_MUTATED' 'control-web source after rebuild'
if (($afterSourceInputs -join "`n") -cne ($sourceInputs -join "`n")) {
    Fail 'CONTROLWEB_SOURCE_MUTATED' 'source file set changed during rebuild'
}
foreach ($relative in $sourceInputs) {
    if ((Get-SHA256 (Join-Path $source $relative)) -cne $before[$relative]) {
        Fail 'CONTROLWEB_SOURCE_MUTATED' "source changed during rebuild: $relative"
    }
}
Write-Output "PASS Test-ControlWeb: npm ci, typecheck, tests, two clean builds, and $($expectedDist.Count) committed assets matched byte-for-byte"
