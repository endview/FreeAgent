[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$checker = Join-Path $PSScriptRoot 'Test-License.ps1'
$projectRoot = Split-Path -Parent $PSScriptRoot
$moduleSum = 'h1:' + ('A' * 43) + '='
$goModSum = 'h1:' + ('B' * 43) + '='
$fixtureNamePrefix = 'free agent ' + [char]0x8bb8 + [char]0x53ef + ' '
$testIsWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$testPathComparison = if ($testIsWindows) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
$passed = 0

function Write-Utf8Canonical([string]$Path, [string]$Content) {
    $normalized = $Content.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd([char]10) + "`n"
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
    [IO.File]::WriteAllText($Path, $normalized, [Text.UTF8Encoding]::new($false))
}

function Write-Utf8Raw([string]$Path, [string]$Content) {
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

function Convert-ToNoticeLegalText([string]$Text) {
    foreach ($character in $Text.ToCharArray()) {
        $value = [int]$character
        $allowedWhitespace = $value -eq 0x09 -or $value -eq 0x0a -or $value -eq 0x0c -or $value -eq 0x0d
        if (($value -lt 0x20 -and -not $allowedWhitespace) -or $value -eq 0x7f -or ($value -ge 0x80 -and $value -le 0x9f)) {
            throw 'legal text contains a forbidden control character'
        }
    }
    return $Text.Replace("`r`n", "`n").Replace("`r", "`n").Replace([string][char]0x0c, "`n").TrimEnd([char]10) + "`n"
}

function Get-MaximumNoticeFenceRun([string]$Text, [char]$Character) {
    $maximum = 0
    $current = 0
    foreach ($candidate in $Text.ToCharArray()) {
        if ($candidate -eq $Character) {
            $current++
            if ($current -gt $maximum) { $maximum = $current }
        } else {
            $current = 0
        }
    }
    return $maximum
}

function Get-NoticeLegalFence([string]$NormalizedText) {
    $backtick = [char]0x60
    $tilde = [char]0x7e
    $backtickLength = [Math]::Max(3, (Get-MaximumNoticeFenceRun $NormalizedText $backtick) + 1)
    $tildeLength = [Math]::Max(3, (Get-MaximumNoticeFenceRun $NormalizedText $tilde) + 1)
    if ($backtickLength -le $tildeLength) { return ([string]$backtick) * $backtickLength }
    return ([string]$tilde) * $tildeLength
}

function Add-NoticeLegalPayload($Lines, [string]$NormalizedText) {
    $fence = Get-NoticeLegalFence $NormalizedText
    [void]$Lines.Add($fence)
    $withoutFinalLF = $NormalizedText.Substring(0, $NormalizedText.Length - 1)
    foreach ($textLine in @($withoutFinalLF.Split([char]10))) { [void]$Lines.Add($textLine) }
    [void]$Lines.Add($fence)
}

function Get-SHA256File([string]$Path) {
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-SHA256Text([string]$Text) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Text)))).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}

function Get-CheckerDependencyManifestPinAssignment([string]$Source) {
    $assignmentNamePattern = '(?m)^[\x20\t]*\$expectedDependencyManifestSHA256[\x20\t]*='
    $assignmentPattern = '(?m)^\$expectedDependencyManifestSHA256 = ''(?<sha256>[0-9a-f]{64})''\r?$'
    $namedAssignments = [regex]::Matches($Source, $assignmentNamePattern)
    if ($namedAssignments.Count -ne 1) {
        throw 'checker must contain exactly one dependency-manifest pin assignment'
    }
    $canonicalAssignments = [regex]::Matches($Source, $assignmentPattern)
    if ($canonicalAssignments.Count -ne 1) {
        throw 'checker dependency-manifest pin assignment must use the canonical lowercase SHA-256 form'
    }
    $shaGroup = $canonicalAssignments[0].Groups['sha256']
    if ($shaGroup.Value -ceq ('0' * 64)) {
        throw 'checker dependency-manifest pin must not use the development placeholder'
    }
    return [pscustomobject]@{
        SHA256 = $shaGroup.Value
        ValueIndex = $shaGroup.Index
        ValueLength = $shaGroup.Length
    }
}

function Set-CheckerDependencyManifestPin([string]$Source, [string]$SHA256) {
    if ($SHA256 -cnotmatch '^[0-9a-f]{64}$') { throw 'fixture dependency-manifest pin must be a lowercase SHA-256 value' }
    $assignment = Get-CheckerDependencyManifestPinAssignment $Source
    return $Source.Substring(0, $assignment.ValueIndex) + $SHA256 + $Source.Substring($assignment.ValueIndex + $assignment.ValueLength)
}

function Get-NoticeID([string]$Kind, [string]$Identity) {
    return $Kind + '-' + (Get-SHA256Text $Identity).Substring(0, 24)
}

function New-RequiredFileRecord([string]$Path, [string]$Role, $Item) {
    return [ordered]@{
        path = $Path
        role = $Role
        size = [int64]$Item.Length
        sha256 = Get-SHA256File $Item.FullName
    }
}

function New-AssetLegalBundle($LicenseItem, $NestedLicenseItem, $NoticeItem, $PatentsItem) {
    return @(
        (New-RequiredFileRecord 'third_party/example/LICENSE' 'license' $LicenseItem),
        (New-RequiredFileRecord 'third_party/example/LICENSES/MIT.txt' 'license' $NestedLicenseItem),
        (New-RequiredFileRecord 'third_party/example/NOTICE' 'notice' $NoticeItem),
        (New-RequiredFileRecord 'third_party/example/PATENTS' 'patent' $PatentsItem)
    )
}

function Get-AssetEntry($Fixture, [string]$Path) {
    $matches = @($Fixture.AssetManifest.assets | Where-Object { [string]$_.path -ceq $Path })
    if ($matches.Count -ne 1) { throw "fixture asset entry is not unique: $Path" }
    return $matches[0]
}

function Sync-AssetFileMetadata($Fixture, [string]$Path) {
    $item = Get-Item -LiteralPath (Join-Path $Fixture.Root $Path)
    foreach ($entry in $Fixture.AssetManifest.assets) {
        if ([string]$entry.path -ceq $Path) {
            $entry.size = [int64]$item.Length
            $entry.sha256 = Get-SHA256File $item.FullName
        }
        foreach ($file in $entry.required_files) {
            if ([string]$file.path -ceq $Path) {
                $file.size = [int64]$item.Length
                $file.sha256 = Get-SHA256File $item.FullName
            }
        }
    }
}

function Sync-DependencyFileMetadata($Fixture, [string]$Path, [string]$Directory = $Fixture.DependencyDirectory) {
    $item = Get-Item -LiteralPath (Join-Path $Directory $Path)
    $matches = @($Fixture.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq $Path })
    if ($matches.Count -ne 1) { throw "fixture dependency required file is not unique: $Path" }
    $matches[0].size = [int64]$item.Length
    $matches[0].sha256 = Get-SHA256File $item.FullName
}

function Write-Json([string]$Path, $Value) {
    Write-Utf8Canonical $Path ($Value | ConvertTo-Json -Depth 10)
}

function Get-TestModuleSourceSPDXScan([string]$Directory) {
    $marker = 'SPDX-License-Identifier:'
    $approved = @(
        '(BSD-3-Clause AND ISC)', '(BSD-4-Clause AND BSD-2-Clause-FreeBSD)',
        'Apache-2.0 WITH LLVM-exception', 'BSD-2-Clause', 'BSD-2-Clause-FreeBSD',
        'BSD-2-Clause-NetBSD', 'BSD-3-Clause', 'BSD-4-Clause',
        'GPL-2.0 WITH Linux-syscall-note', 'GPL-2.0+ WITH Linux-syscall-note',
        'GPL-2.0-only WITH Linux-syscall-note', 'MIT', 'MPL-2.0'
    )
    $paths = New-Object System.Collections.Generic.List[string]
    foreach ($item in @(Get-ChildItem -LiteralPath $Directory -File -Force -Recurse)) {
        $relative = $item.FullName.Substring($Directory.TrimEnd([IO.Path]::DirectorySeparatorChar).Length + 1).Replace([char]92, [char]47)
        $paths.Add($relative)
    }
    $orderedPaths = [string[]]@($paths)
    [Array]::Sort($orderedPaths, [StringComparer]::Ordinal)
    $records = New-Object Text.StringBuilder
    $expressions = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    [int64]$count = 0
    $strictUTF8 = New-Object Text.UTF8Encoding($false, $true)
    foreach ($relative in $orderedPaths) {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $Directory $relative))
        $ascii = [Text.Encoding]::ASCII.GetString($bytes)
        if ($ascii.IndexOf($marker, [StringComparison]::Ordinal) -lt 0) { continue }
        $text = $strictUTF8.GetString($bytes).Replace("`r`n", "`n").Replace("`r", "`n")
        $lines = $text.Split([char]10)
        for ($lineIndex = 0; $lineIndex -lt $lines.Length; $lineIndex++) {
            $line = $lines[$lineIndex]
            $offset = 0
            $occurrenceOnLine = 0
            while ($offset -le $line.Length - $marker.Length) {
                $markerOffset = $line.IndexOf($marker, $offset, [StringComparison]::Ordinal)
                if ($markerOffset -lt 0) { break }
                $occurrenceOnLine++
                $expressionStart = $markerOffset + $marker.Length
                $nextMarker = $line.IndexOf($marker, $expressionStart, [StringComparison]::Ordinal)
                $expressionEnd = if ($nextMarker -lt 0) { $line.Length } else { $nextMarker }
                $expression = $line.Substring($expressionStart, $expressionEnd - $expressionStart).Trim([char[]]@([char]0x20, [char]0x09))
                if ($approved -cnotcontains $expression) { throw "test fixture contains unsupported SPDX expression: $expression" }
                $count++
                [void]$expressions.Add($expression)
                [void]$records.Append("${relative}`t$($lineIndex + 1)`t${occurrenceOnLine}`t${expression}`n")
                if ($nextMarker -lt 0) { break }
                $offset = $nextMarker
            }
        }
    }
    $orderedExpressions = [string[]]@($expressions)
    [Array]::Sort($orderedExpressions, [StringComparer]::Ordinal)
    return [ordered]@{
        algorithm = 'raw-ascii-marker-path-line-v1'
        occurrence_count = $count
        sha256 = Get-SHA256Text $records.ToString()
        expressions = @($orderedExpressions)
    }
}

function Sync-DependencySPDXScan($Fixture) {
    $Fixture.DependencyManifest.modules[0].source_spdx_scan = Get-TestModuleSourceSPDXScan $Fixture.DependencyDirectory
}

function Write-PinnedCheckerCopy($Fixture) {
    $source = [IO.File]::ReadAllText($checker, [Text.Encoding]::UTF8)
    $pinned = Set-CheckerDependencyManifestPin $source (Get-SHA256File $Fixture.DependencyPath)
    [IO.File]::WriteAllText($Fixture.CheckerPath, $pinned, [Text.UTF8Encoding]::new($false))
}

function Write-DependencyManifestAndPin($Fixture) {
    Write-Json $Fixture.DependencyPath $Fixture.DependencyManifest
    Write-PinnedCheckerCopy $Fixture
}

function New-FakeGo([string]$Root, [string]$DependencyDirectory) {
    $path = Join-Path $Root 'fake go.ps1'
    $rootLiteral = $Root.Replace("'", "''")
    $dependencyLiteral = $DependencyDirectory.Replace("'", "''")
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
$fixtureRoot = '__ROOT__'
$dependencyDirectory = '__DEPENDENCY__'
$moduleSum = '__MODULE_SUM__'
$goModSum = '__GO_MOD_SUM__'
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output (Join-Path $fixtureRoot 'go.mod')
    return
}
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'mod' -and $GoArgs[1] -ceq 'verify') {
    Write-Output 'all modules verified'
    return
}
if ($GoArgs.Count -eq 5 -and $GoArgs[0] -ceq 'list' -and $GoArgs[1] -ceq '-mod=readonly' -and $GoArgs[2] -ceq '-m' -and $GoArgs[3] -ceq '-json' -and $GoArgs[4] -ceq 'all') {
    [pscustomobject]@{ Path = 'github.com/endview/freeagent'; Main = $true; Dir = $fixtureRoot; GoMod = (Join-Path $fixtureRoot 'go.mod') } |
        ConvertTo-Json | Write-Output
    [pscustomobject]@{
        Path = 'example.org/dep'
        Version = 'v1.2.3'
        Dir = $dependencyDirectory
        GoMod = (Join-Path $dependencyDirectory 'go.mod')
        Sum = $moduleSum
        GoModSum = $goModSum
    } | ConvertTo-Json | Write-Output
    return
}
throw "unexpected fake Go arguments: $($GoArgs -join ' ')"
'@
    $content = $content.Replace('__ROOT__', $rootLiteral).Replace('__DEPENDENCY__', $dependencyLiteral).Replace('__MODULE_SUM__', $moduleSum).Replace('__GO_MOD_SUM__', $goModSum)
    # Windows PowerShell 5.1 requires a BOM to decode non-ASCII script paths reliably.
    [IO.File]::WriteAllText($path, $content.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd([char]10) + "`n", [Text.UTF8Encoding]::new($true))
    return $path
}

function New-FakeGoMainOnly([string]$Root) {
    $path = Join-Path $Root 'fake go.ps1'
    $rootLiteral = $Root.Replace("'", "''")
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
$fixtureRoot = '__ROOT__'
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output (Join-Path $fixtureRoot 'go.mod')
    return
}
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'mod' -and $GoArgs[1] -ceq 'verify') {
    Write-Output 'all modules verified'
    return
}
if ($GoArgs.Count -eq 5 -and $GoArgs[0] -ceq 'list' -and $GoArgs[1] -ceq '-mod=readonly' -and $GoArgs[2] -ceq '-m' -and $GoArgs[3] -ceq '-json' -and $GoArgs[4] -ceq 'all') {
    [pscustomobject]@{ Path = 'github.com/endview/freeagent'; Main = $true; Dir = $fixtureRoot; GoMod = (Join-Path $fixtureRoot 'go.mod') } |
        ConvertTo-Json | Write-Output
    return
}
throw "unexpected fake Go arguments: $($GoArgs -join ' ')"
'@
    $content = $content.Replace('__ROOT__', $rootLiteral)
    [IO.File]::WriteAllText($path, $content.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd([char]10) + "`n", [Text.UTF8Encoding]::new($true))
    return $path
}

function New-FakeGoModuleStream([string]$Root, [string]$ModuleStream) {
    $path = Join-Path $Root 'fake go.ps1'
    $rootLiteral = $Root.Replace("'", "''")
    $streamBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($ModuleStream))
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
$fixtureRoot = '__ROOT__'
$streamBase64 = '__STREAM_BASE64__'
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output (Join-Path $fixtureRoot 'go.mod')
    return
}
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'mod' -and $GoArgs[1] -ceq 'verify') {
    Write-Output 'all modules verified'
    return
}
if ($GoArgs.Count -eq 5 -and $GoArgs[0] -ceq 'list' -and $GoArgs[1] -ceq '-mod=readonly' -and $GoArgs[2] -ceq '-m' -and $GoArgs[3] -ceq '-json' -and $GoArgs[4] -ceq 'all') {
    Write-Output ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($streamBase64)))
    return
}
throw "unexpected fake Go arguments: $($GoArgs -join ' ')"
'@
    $content = $content.Replace('__ROOT__', $rootLiteral).Replace('__STREAM_BASE64__', $streamBase64)
    [IO.File]::WriteAllText($path, $content.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd([char]10) + "`n", [Text.UTF8Encoding]::new($true))
    return $path
}

function New-FakeGoVerifyPolicy([string]$Root, [string]$DependencyDirectory, [string]$Mode, [string]$SentinelPath = '', [string]$UntrustedDiagnostic = '') {
    $path = Join-Path $Root 'fake go.ps1'
    $rootLiteral = $Root.Replace("'", "''")
    $dependencyLiteral = $DependencyDirectory.Replace("'", "''")
    $sentinelLiteral = $SentinelPath.Replace("'", "''")
    $diagnosticLiteral = $UntrustedDiagnostic.Replace("'", "''")
    $expectedSentinelHash = if ([string]::IsNullOrWhiteSpace($SentinelPath)) { '' } else { Get-SHA256File $SentinelPath }
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
$fixtureRoot = '__ROOT__'
$dependencyDirectory = '__DEPENDENCY__'
$mode = '__MODE__'
$sentinelPath = '__SENTINEL__'
$expectedSentinelHash = '__SENTINEL_HASH__'
$untrustedDiagnostic = '__DIAGNOSTIC__'
$statePath = Join-Path $fixtureRoot '.verify-state'
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output (Join-Path $fixtureRoot 'go.mod')
    return
}
if ($GoArgs.Count -eq 5 -and $GoArgs[0] -ceq 'list' -and $GoArgs[1] -ceq '-mod=readonly' -and $GoArgs[2] -ceq '-m' -and $GoArgs[3] -ceq '-json' -and $GoArgs[4] -ceq 'all') {
    [pscustomobject]@{ Path = 'github.com/endview/freeagent'; Main = $true; Dir = $fixtureRoot; GoMod = (Join-Path $fixtureRoot 'go.mod') } | ConvertTo-Json | Write-Output
    [pscustomobject]@{ Path = 'example.org/dep'; Version = 'v1.2.3'; Dir = $dependencyDirectory; GoMod = (Join-Path $dependencyDirectory 'go.mod'); Sum = '__MODULE_SUM__'; GoModSum = '__GO_MOD_SUM__' } | ConvertTo-Json | Write-Output
    return
}
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'mod' -and $GoArgs[1] -ceq 'verify') {
    $count = if (Test-Path -LiteralPath $statePath) { [int][IO.File]::ReadAllText($statePath) } else { 0 }
    $count++
    [IO.File]::WriteAllText($statePath, [string]$count)
    if ($mode -ceq 'pre-fail') { throw "verify failed $untrustedDiagnostic" }
    if ($mode -ceq 'post-fail' -and $count -ge 2) { throw "verify failed $untrustedDiagnostic" }
    if ($mode -ceq 'mutate-go-mod' -and $count -eq 1) {
        [IO.File]::AppendAllText((Join-Path $fixtureRoot 'go.mod'), "`n// mutated")
        return
    }
    if ($mode -ceq 'tamper-after-pre') {
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $sentinelPath).Hash.ToLowerInvariant()
        if ($actual -cne $expectedSentinelHash) { throw "module cache verification failed $untrustedDiagnostic" }
        if ($count -eq 1) { [IO.File]::AppendAllText($sentinelPath, 'tampered') }
    }
    return
}
throw "unexpected fake Go arguments: $($GoArgs -join ' ')"
'@
    $content = $content.Replace('__ROOT__', $rootLiteral).Replace('__DEPENDENCY__', $dependencyLiteral).Replace('__MODE__', $Mode).Replace('__SENTINEL__', $sentinelLiteral).Replace('__SENTINEL_HASH__', $expectedSentinelHash).Replace('__DIAGNOSTIC__', $diagnosticLiteral).Replace('__MODULE_SUM__', $moduleSum).Replace('__GO_MOD_SUM__', $goModSum)
    [IO.File]::WriteAllText($path, $content.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd([char]10) + "`n", [Text.UTF8Encoding]::new($true))
    return $path
}

function New-ExpectedNotices($DependencyManifest, $AssetManifest, [string]$DependencyPath, [string]$AssetPath, [string]$DependencyDirectory, [string]$RepositoryRoot) {
    $rows = @()
    foreach ($entry in $DependencyManifest.modules) {
        $rows += "| $($entry.notice_id) | go-module | $($entry.path) | $($entry.version) | $($entry.declared_license_expression) | $($entry.source_url) |"
    }
    foreach ($entry in $AssetManifest.assets) {
        if ($entry.origin -ceq 'third-party') {
            $rows += "| $($entry.notice_id) | distributed-asset | $($entry.path) | - | $($entry.spdx_expression) | $($entry.source_url) |"
        }
    }
    $lines = New-Object System.Collections.Generic.List[string]
    foreach ($line in @(
        '# Third-Party Notices',
        '',
        'This file is validated by `scripts/Test-License.ps1`.',
        '',
        '<!-- freeagent-license-notices-v1 -->',
        "dependency-manifest-sha256: $(Get-SHA256File $DependencyPath)",
        "distributed-assets-manifest-sha256: $(Get-SHA256File $AssetPath)",
        "notice-count: $($rows.Count)",
        '',
        '| Notice ID | Kind | Component | Version | Declared SPDX | Source |',
        '| --- | --- | --- | --- | --- | --- |'
    )) { $lines.Add($line) }
    foreach ($row in $rows) { $lines.Add($row) }
    $lines.Add('')
    $lines.Add('## Dependency License Texts')
    $lines.Add('')
    foreach ($entry in $DependencyManifest.modules) {
        $lines.Add("### $($entry.notice_id)")
        $lines.Add('')
        $lines.Add("Component: $($entry.path)@$($entry.version)")
        $lines.Add('')
        $lines.Add("Declared SPDX: $($entry.declared_license_expression)")
        $lines.Add('')
        $lines.Add("Compatibility: $($entry.compatibility_conclusion.status) ($($entry.compatibility_conclusion.policy_id)@$($entry.compatibility_conclusion.policy_version))")
        $lines.Add('')
        $detectedExpressions = if ($entry.source_spdx_scan.expressions.Count -eq 0) { '(none)' } else { @($entry.source_spdx_scan.expressions) -join ', ' }
        $lines.Add("Detected SPDX expressions: $detectedExpressions")
        $lines.Add('')
        $lines.Add("Detected SPDX occurrence count: $($entry.source_spdx_scan.occurrence_count)")
        $lines.Add('')
        $lines.Add("Detected SPDX digest: $($entry.source_spdx_scan.sha256)")
        $lines.Add('')
        foreach ($file in $entry.required_files) {
            $text = Convert-ToNoticeLegalText ([IO.File]::ReadAllText((Join-Path $DependencyDirectory $file.path), [Text.Encoding]::UTF8))
            $lines.Add("#### $($file.role): $($file.path)")
            $lines.Add('')
            $lines.Add("SHA-256: $($file.sha256)")
            $lines.Add('')
            Add-NoticeLegalPayload $lines $text
            $lines.Add('')
        }
    }
    $lines.Add('## Distributed-Asset License Texts')
    $lines.Add('')
    foreach ($entry in $AssetManifest.assets) {
        if ([string]$entry.origin -cne 'third-party') { continue }
        $lines.Add("### $($entry.notice_id)")
        $lines.Add('')
        $lines.Add("Component: $($entry.path)")
        $lines.Add('')
        foreach ($file in $entry.required_files) {
            $text = Convert-ToNoticeLegalText ([IO.File]::ReadAllText((Join-Path $RepositoryRoot $file.path), [Text.Encoding]::UTF8))
            $lines.Add("#### $($file.role): $($file.path)")
            $lines.Add('')
            $lines.Add("SHA-256: $($file.sha256)")
            $lines.Add('')
            Add-NoticeLegalPayload $lines $text
            $lines.Add('')
        }
    }
    return @($lines) -join "`n"
}

function Write-ManifestsAndNotice($Fixture) {
    Write-Json $Fixture.DependencyPath $Fixture.DependencyManifest
    Write-Json $Fixture.AssetPath $Fixture.AssetManifest
    Write-PinnedCheckerCopy $Fixture
    Write-Utf8Canonical $Fixture.NoticesPath (New-ExpectedNotices $Fixture.DependencyManifest $Fixture.AssetManifest $Fixture.DependencyPath $Fixture.AssetPath $Fixture.DependencyDirectory $Fixture.Root)
}

function New-ValidFixture {
    $root = Join-Path ([IO.Path]::GetTempPath()) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $root | Out-Null
    foreach ($directory in @('docs', 'examples', 'internal', 'testdata', 'testdata/release', 'third_party/example/LICENSES', '.fixture-modcache/example.org/dep@v1.2.3/LICENSES')) {
        New-Item -ItemType Directory -Path (Join-Path $root $directory) -Force | Out-Null
    }
    [IO.File]::Copy((Join-Path $projectRoot 'LICENSE'), (Join-Path $root 'LICENSE'))
    Write-Utf8Canonical (Join-Path $root 'README.md') "# Fixture`n`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
    Write-Utf8Canonical (Join-Path $root 'README.zh-CN.md') "# Fixture`n`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
    Write-Utf8Canonical (Join-Path $root 'README.zh-TW.md') "# Fixture`n`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
    Write-Utf8Canonical (Join-Path $root 'CONTRIBUTING.md') 'Contributions use `AGPL-3.0-only`.'
    Write-Utf8Canonical (Join-Path $root 'docs/PRD.md') 'License: AGPL-3.0-only'
    Write-Utf8Canonical (Join-Path $root 'go.mod') "module github.com/endview/freeagent`n`ngo 1.26.5`n`nrequire example.org/dep v1.2.3"
    Write-Utf8Canonical (Join-Path $root 'go.sum') "example.org/dep v1.2.3 $moduleSum`nexample.org/dep v1.2.3/go.mod $goModSum"
    Write-Utf8Canonical (Join-Path $root 'examples/project.json') '{}'
    Write-Utf8Canonical (Join-Path $root 'internal/project.go') '// SPDX-License-Identifier: AGPL-3.0-only'
    Write-Utf8Canonical (Join-Path $root 'testdata/external.txt') 'external fixture'
    Write-Utf8Raw (Join-Path $root 'third_party/example/LICENSE') "Example asset license`r`n`r`n"
    Write-Utf8Canonical (Join-Path $root 'third_party/example/LICENSES/MIT.txt') 'Example nested asset license'
    Write-Utf8Canonical (Join-Path $root 'third_party/example/NOTICE') 'Example asset notice'
    Write-Utf8Canonical (Join-Path $root 'third_party/example/PATENTS') 'Example asset patent grant'
    Write-Utf8Canonical (Join-Path $root 'third_party/example/tool.go') "// SPDX-License-Identifier: MIT`npackage example"

    $dependencyDirectory = Join-Path $root '.fixture-modcache/example.org/dep@v1.2.3'
    Write-Utf8Canonical (Join-Path $dependencyDirectory 'go.mod') "module example.org/dep`n`ngo 1.20"
    Write-Utf8Raw (Join-Path $dependencyDirectory 'LICENSE') 'Example dependency license'
    Write-Utf8Canonical (Join-Path $dependencyDirectory 'LICENSES/Apache-2.0.txt') 'Example nested dependency license'
    Write-Utf8Canonical (Join-Path $dependencyDirectory 'NOTICE') 'Example dependency notice'
    Write-Utf8Canonical (Join-Path $dependencyDirectory 'PATENTS') 'Example dependency patent grant'
    Write-Utf8Canonical (Join-Path $dependencyDirectory 'source.txt') '// SPDX-License-Identifier: MIT'
    $licenseFile = Get-Item -LiteralPath (Join-Path $dependencyDirectory 'LICENSE')
    $nestedLicenseFile = Get-Item -LiteralPath (Join-Path $dependencyDirectory 'LICENSES/Apache-2.0.txt')
    $noticeFile = Get-Item -LiteralPath (Join-Path $dependencyDirectory 'NOTICE')
    $patentsFile = Get-Item -LiteralPath (Join-Path $dependencyDirectory 'PATENTS')
    $projectAsset = Get-Item -LiteralPath (Join-Path $root 'examples/project.json')
    $externalAsset = Get-Item -LiteralPath (Join-Path $root 'testdata/external.txt')
    $assetLicense = Get-Item -LiteralPath (Join-Path $root 'third_party/example/LICENSE')
    $assetNestedLicense = Get-Item -LiteralPath (Join-Path $root 'third_party/example/LICENSES/MIT.txt')
    $assetNotice = Get-Item -LiteralPath (Join-Path $root 'third_party/example/NOTICE')
    $assetPatents = Get-Item -LiteralPath (Join-Path $root 'third_party/example/PATENTS')
    $thirdPartyGo = Get-Item -LiteralPath (Join-Path $root 'third_party/example/tool.go')

    $dependencyEntry = [ordered]@{
        path = 'example.org/dep'
        version = 'v1.2.3'
        module_sum = $moduleSum
        go_mod_sum = $goModSum
        declared_license_expression = 'MIT'
        source_url = 'https://example.invalid/module'
        notice_id = Get-NoticeID 'go' 'example.org/dep@v1.2.3'
        compatibility_conclusion = [ordered]@{
            status = 'reviewed-compatible-for-policy'
            policy_id = 'freeagent-agpl-3.0-unvendored-source-six-cgo0-binaries'
            policy_version = 1
        }
        source_spdx_scan = Get-TestModuleSourceSPDXScan $dependencyDirectory
        required_files = @(
            (New-RequiredFileRecord 'LICENSE' 'license' $licenseFile),
            (New-RequiredFileRecord 'LICENSES/Apache-2.0.txt' 'license' $nestedLicenseFile),
            (New-RequiredFileRecord 'NOTICE' 'notice' $noticeFile),
            (New-RequiredFileRecord 'PATENTS' 'patent' $patentsFile)
        )
    }
    $dependencyManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-go-dependency-licenses'
        main_module = 'github.com/endview/freeagent'
        compatibility_policy = [ordered]@{
            id = 'freeagent-agpl-3.0-unvendored-source-six-cgo0-binaries'
            version = 1
            project_license = 'AGPL-3.0-only'
            source_dependency_mode = 'module-reference-only'
            binary_targets = @('darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64')
            cgo_enabled = $false
            go_version = 'go1.26.5'
        }
        license_refs = @()
        modules = @($dependencyEntry)
    }
    $assetManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-distributed-assets'
        assets = @(
            [ordered]@{
                path = 'examples/project.json'
                size = [int64]$projectAsset.Length
                sha256 = Get-SHA256File $projectAsset.FullName
                origin = 'project-owned'
                source_url = 'project://github.com/endview/freeagent'
                spdx_expression = 'AGPL-3.0-only'
                notice_id = ''
                required_files = @()
            },
            [ordered]@{
                path = 'testdata/external.txt'
                size = [int64]$externalAsset.Length
                sha256 = Get-SHA256File $externalAsset.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/asset'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'testdata/external.txt'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            },
            [ordered]@{
                path = 'third_party/example/LICENSE'
                size = [int64]$assetLicense.Length
                sha256 = Get-SHA256File $assetLicense.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/asset-license'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'third_party/example/LICENSE'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            },
            [ordered]@{
                path = 'third_party/example/LICENSES/MIT.txt'
                size = [int64]$assetNestedLicense.Length
                sha256 = Get-SHA256File $assetNestedLicense.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/asset-nested-license'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'third_party/example/LICENSES/MIT.txt'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            },
            [ordered]@{
                path = 'third_party/example/NOTICE'
                size = [int64]$assetNotice.Length
                sha256 = Get-SHA256File $assetNotice.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/asset-notice'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'third_party/example/NOTICE'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            },
            [ordered]@{
                path = 'third_party/example/PATENTS'
                size = [int64]$assetPatents.Length
                sha256 = Get-SHA256File $assetPatents.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/asset-patents'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'third_party/example/PATENTS'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            },
            [ordered]@{
                path = 'third_party/example/tool.go'
                size = [int64]$thirdPartyGo.Length
                sha256 = Get-SHA256File $thirdPartyGo.FullName
                origin = 'third-party'
                source_url = 'https://example.invalid/tool'
                spdx_expression = 'MIT'
                notice_id = Get-NoticeID 'asset' 'third_party/example/tool.go'
                required_files = New-AssetLegalBundle $assetLicense $assetNestedLicense $assetNotice $assetPatents
            }
        )
    }
    $fixture = [pscustomobject]@{
        Root = $root
        DependencyDirectory = $dependencyDirectory
        DependencyPath = Join-Path $root 'testdata/release/dependency-licenses.v1.json'
        AssetPath = Join-Path $root 'testdata/release/distributed-assets.v1.json'
        NoticesPath = Join-Path $root 'THIRD_PARTY_NOTICES.md'
        CheckerPath = Join-Path $root 'test license checker.ps1'
        DependencyManifest = $dependencyManifest
        AssetManifest = $assetManifest
        GoCommand = $null
        CleanupRoots = @()
        CleanupLinks = @()
        SensitivePaths = @()
    }
    Write-ManifestsAndNotice $fixture
    $fixture.GoCommand = New-FakeGo $root $dependencyDirectory
    return $fixture
}

function Remove-Fixture([string]$Root) {
    $full = [IO.Path]::GetFullPath($Root)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($temp, $testPathComparison) -or -not (Split-Path -Leaf $full).StartsWith($fixtureNamePrefix, [StringComparison]::Ordinal)) {
        throw "refusing to delete unexpected fixture path: $full"
    }
    if (Test-Path -LiteralPath $full) {
        $queue = New-Object 'Collections.Generic.Queue[string]'
        $queue.Enqueue($full)
        while ($queue.Count -ne 0) {
            $directory = $queue.Dequeue()
            foreach ($item in @(Get-ChildItem -LiteralPath $directory -Force)) {
                if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                    if ($item.PSIsContainer) { [IO.Directory]::Delete($item.FullName, $false) } else { [IO.File]::Delete($item.FullName) }
                } elseif ($item.PSIsContainer) {
                    $queue.Enqueue($item.FullName)
                }
            }
        }
        Remove-Item -LiteralPath $full -Recurse -Force
    }
}

function Remove-TestLink([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($temp, $testPathComparison) -or -not (Split-Path -Leaf $full).StartsWith($fixtureNamePrefix, [StringComparison]::Ordinal)) {
        throw "refusing to remove unexpected fixture link: $full"
    }
    if (-not (Test-Path -LiteralPath $full)) { return }
    $item = Get-Item -Force -LiteralPath $full
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { throw "refusing to remove a non-reparse fixture link: $full" }
    if ($item.PSIsContainer) { [IO.Directory]::Delete($full, $false) } else { [IO.File]::Delete($full) }
}

function Get-CurrentPowerShellEnginePath {
    $engineName = if ($PSVersionTable.PSEdition -ceq 'Core') {
        if ($testIsWindows) { 'pwsh.exe' } else { 'pwsh' }
    } else {
        'powershell.exe'
    }
    $enginePath = [IO.Path]::GetFullPath((Join-Path $PSHOME $engineName))
    if (-not (Test-Path -LiteralPath $enginePath -PathType Leaf)) { throw "current PowerShell engine is unavailable: $enginePath" }
    return $enginePath
}

function New-UnixFifo([string]$Path) {
    if ($testIsWindows) { throw 'Unix FIFO fixture is unavailable on Windows' }
    $process = New-Object Diagnostics.Process
    try {
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = '/usr/bin/mkfifo'
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        if ($null -eq $start.ArgumentList) { throw 'Unix FIFO fixture requires ProcessStartInfo.ArgumentList' }
        [void]$start.ArgumentList.Add('--')
        [void]$start.ArgumentList.Add($Path)
        $start.EnvironmentVariables['LC_ALL'] = 'C'
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'Unable to start the Unix FIFO fixture tool' }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(5000)) {
            try { $process.Kill() } catch { }
            if (-not $process.WaitForExit(5000)) { throw 'Unable to terminate the Unix FIFO fixture tool' }
            throw 'Unix FIFO fixture tool timed out'
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        if ($process.ExitCode -ne 0 -or -not [string]::IsNullOrEmpty($stdout) -or -not [string]::IsNullOrEmpty($stderr)) {
            throw 'Unable to create the Unix FIFO fixture'
        }
    } finally {
        $process.Dispose()
    }
}

function Invoke-CheckerProcess([string]$Root, [string]$GoCommand, [int]$TimeoutMilliseconds = 15000, [string]$CheckerPath = $checker) {
    if ($TimeoutMilliseconds -lt 1) { throw 'checker timeout must be positive' }
    $quotedChecker = $CheckerPath.Replace("'", "''")
    $quotedRoot = $Root.Replace("'", "''")
    $command = "`$ProgressPreference = 'SilentlyContinue'; & '$quotedChecker' -Root '$quotedRoot'"
    if (-not [string]::IsNullOrWhiteSpace($GoCommand)) {
        $command += " -GoCommand '$($GoCommand.Replace("'", "''"))'"
    }
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $enginePath = Get-CurrentPowerShellEnginePath
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $enginePath
    $start.Arguments = "-NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand $encoded"
    $start.WorkingDirectory = [IO.Path]::GetTempPath()
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        [void]$process.Start()
        $processID = $process.Id
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timedOut = -not $process.WaitForExit($TimeoutMilliseconds)
        if ($timedOut) {
            try { $process.Kill() } catch { throw "failed to terminate timed-out checker: $($_.Exception.Message)" }
            if (-not $process.WaitForExit(5000)) { throw 'timed-out checker did not terminate within the kill grace period' }
        } else {
            $process.WaitForExit()
        }
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        return [pscustomobject]@{ ExitCode = $process.ExitCode; Stdout = $stdout; Stderr = $stderr; TimedOut = $timedOut; ProcessId = $processID; EnginePath = $enginePath }
    } finally {
        $process.Dispose()
    }
}

function Invoke-DocsProbeProcess([string]$Root, [int]$TimeoutMilliseconds = 30000) {
    $docsChecker = Join-Path $PSScriptRoot 'Test-Docs.ps1'
    $quotedChecker = $docsChecker.Replace("'", "''")
    $quotedRoot = $Root.Replace("'", "''")
    $command = "`$ProgressPreference = 'SilentlyContinue'; & '$quotedChecker' -Root '$quotedRoot'"
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = Get-CurrentPowerShellEnginePath
    $start.Arguments = "-NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand $encoded"
    $start.WorkingDirectory = [IO.Path]::GetTempPath()
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        [void]$process.Start()
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timedOut = -not $process.WaitForExit($TimeoutMilliseconds)
        if ($timedOut) {
            try { $process.Kill() } catch { }
            if (-not $process.WaitForExit(5000)) { throw 'timed-out docs probe did not terminate within the kill grace period' }
        } else {
            $process.WaitForExit()
        }
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdoutTask.Result
            Stderr = $stderrTask.Result
            TimedOut = $timedOut
        }
    } finally {
        $process.Dispose()
    }
}

function Assert-CleanCheckerOutput($Result, [string]$FixtureRoot, [string]$FixtureGoCommand, [string[]]$AdditionalSensitivePaths = @()) {
    $combined = $Result.Stdout + "`n" + $Result.Stderr
    $userHome = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    foreach ($secretPath in @($FixtureRoot, $FixtureGoCommand, $checker, $userHome, $tempRoot) + $AdditionalSensitivePaths) {
        if (-not [string]::IsNullOrWhiteSpace($secretPath) -and $combined.IndexOf($secretPath, $testPathComparison) -ge 0) {
            throw "checker output leaked an absolute local path"
        }
    }
    if ($combined -match '(?im)(?<![A-Za-z0-9._@+/-])file:[\\/]+[^\r\n]*' -or
        $combined -match '(?im)(?:(?<![A-Za-z0-9._@+/-])[A-Z]:[\\/]|\\\\)[^\r\n]*' -or
        $combined -match '(?m)(?<![A-Za-z0-9:/])//[^\r\n]*' -or
        $combined -match '(?m)(?<![A-Za-z0-9._@+:/-])/(?!/)[^\r\n]*') {
        throw 'checker output leaked an unrecognized absolute path'
    }
    if ($combined -match '(?im)#<\s*CLIXML|<Objs\s+Version=|StackTrace|CategoryInfo|FullyQualifiedErrorId|^\s*At\s+.+Test-License\.ps1:') {
        throw 'checker output leaked a PowerShell stack trace'
    }
}

function Invoke-Checker($Fixture) {
    $result = Invoke-CheckerProcess $Fixture.Root $Fixture.GoCommand 15000 $Fixture.CheckerPath
    Assert-CleanCheckerOutput $result $Fixture.Root $Fixture.GoCommand $Fixture.SensitivePaths
    if ($result.TimedOut) { throw '[LICENSE_TEST_TIMEOUT] checker process exceeded the finite timeout' }
    if ($result.ExitCode -ne 0) {
        $message = $result.Stderr.Trim()
        if ([string]::IsNullOrWhiteSpace($message)) { $message = $result.Stdout.Trim() }
        throw $message
    }
    return @($result.Stdout -split "`r?`n" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}

function Assert-Fails($Fixture, [string[]]$Codes) {
    try {
        [void](Invoke-Checker $Fixture)
    } catch {
        foreach ($code in $Codes) {
            if ($_.Exception.Message.IndexOf("[$code]", [StringComparison]::Ordinal) -lt 0) {
                throw "expected [$code], got: $($_.Exception.Message)"
            }
        }
        return
    }
    throw "expected failure: $($Codes -join ', ')"
}

function Invoke-Case([string]$Name, [scriptblock]$Mutation, [string[]]$ExpectedCodes) {
    $fixture = New-ValidFixture
    try {
        if ($null -ne $Mutation) { & $Mutation $fixture }
        if ($ExpectedCodes.Count -eq 0) {
            $output = Invoke-Checker $fixture
            if (@($output | Where-Object { $_ -like 'PASS Test-License:*' }).Count -ne 1) {
                throw "valid fixture did not emit exactly one PASS line: $($output -join ' ')"
            }
        } else {
            Assert-Fails $fixture $ExpectedCodes
        }
        $script:passed++
        Write-Output "PASS fixture: $Name"
    } finally {
        foreach ($cleanupLink in @($fixture.CleanupLinks)) { Remove-TestLink $cleanupLink }
        Remove-Fixture $fixture.Root
        foreach ($cleanupRoot in @($fixture.CleanupRoots)) { Remove-Fixture $cleanupRoot }
    }
}

function Invoke-DiagnosticCase([string]$Name, [scriptblock]$Mutation, [string]$ExpectedCode, [string[]]$ExpectedFragments) {
    $fixture = New-ValidFixture
    try {
        & $Mutation $fixture
        $result = Invoke-CheckerProcess $fixture.Root $fixture.GoCommand 15000 $fixture.CheckerPath
        Assert-CleanCheckerOutput $result $fixture.Root $fixture.GoCommand $fixture.SensitivePaths
        if ($result.TimedOut) { throw "diagnostic fixture timed out: $Name" }
        if ($result.ExitCode -eq 0) { throw "diagnostic fixture unexpectedly passed: $Name" }
        if ($result.Stderr.IndexOf("[$ExpectedCode]", [StringComparison]::Ordinal) -lt 0) {
            throw "diagnostic fixture expected [$ExpectedCode], got: $($result.Stderr)"
        }
        foreach ($fragment in $ExpectedFragments) {
            if ($result.Stderr.IndexOf($fragment, [StringComparison]::Ordinal) -lt 0) {
                throw "diagnostic fixture did not preserve '$fragment': $($result.Stderr)"
            }
        }
        $script:passed++
        Write-Output "PASS fixture: $Name"
    } finally {
        foreach ($cleanupLink in @($fixture.CleanupLinks)) { Remove-TestLink $cleanupLink }
        Remove-Fixture $fixture.Root
        foreach ($cleanupRoot in @($fixture.CleanupRoots)) { Remove-Fixture $cleanupRoot }
    }
}

$checkerTokens = $null
$checkerParseErrors = $null
$checkerAst = [Management.Automation.Language.Parser]::ParseFile($checker, [ref]$checkerTokens, [ref]$checkerParseErrors)
if ($checkerParseErrors.Count -ne 0) { throw 'checker cannot be parsed for reserved-variable smoke validation' }
$reservedIsWindowsReferences = @($checkerAst.FindAll({
    param($node)
    return $node -is [Management.Automation.Language.VariableExpressionAst] -and $node.VariablePath.UserPath -ieq 'IsWindows'
}, $true))
if ($reservedIsWindowsReferences.Count -ne 0) { throw 'checker collides with the PowerShell 7 read-only $IsWindows automatic variable' }
$checkerSource = [IO.File]::ReadAllText($checker, [Text.Encoding]::UTF8)
$productionPin = Get-CheckerDependencyManifestPinAssignment $checkerSource
$productionDependencyManifest = Join-Path $projectRoot 'testdata/release/dependency-licenses.v1.json'
if (-not (Test-Path -LiteralPath $productionDependencyManifest -PathType Leaf)) { throw 'production dependency-license manifest is missing' }
$productionDependencyManifestSHA256 = Get-SHA256File $productionDependencyManifest
if ($productionPin.SHA256 -cne $productionDependencyManifestSHA256) { throw 'checker dependency-manifest pin does not match the production manifest SHA-256' }
$copyProbeSHA256 = 'f' * 64
$copyProbeSource = $checkerSource + "`n# dependency-manifest pin copy probe: $($productionPin.SHA256)`n"
$copyProbeResult = Set-CheckerDependencyManifestPin $copyProbeSource $copyProbeSHA256
if ($copyProbeResult.IndexOf("# dependency-manifest pin copy probe: $($productionPin.SHA256)", [StringComparison]::Ordinal) -lt 0) {
    throw 'fixture checker pin replacement modified a non-assignment SHA-256 value'
}
$copyProbeAssignment = Get-CheckerDependencyManifestPinAssignment $copyProbeResult
if ($copyProbeAssignment.SHA256 -cne $copyProbeSHA256) { throw 'fixture checker pin replacement did not update the assignment value' }
$checkerParameterNames = @($checkerAst.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
if (($checkerParameterNames -join "`n") -cne "Root`nGoCommand") { throw 'checker must not expose a dependency-manifest pin bypass parameter' }
if ($checkerSource -match '(?i)env:.*(?:dependency|manifest).*(?:pin|sha)') { throw 'checker must not expose an environment-based dependency-manifest pin bypass' }
$selfSource = [IO.File]::ReadAllText($PSCommandPath, [Text.Encoding]::UTF8)
$pathPriorityNeedle = 'Get-' + 'Command pwsh'
if ($selfSource.IndexOf($pathPriorityNeedle, [StringComparison]::OrdinalIgnoreCase) -ge 0) { throw 'checker subprocess must not select a different PowerShell engine from PATH' }
$script:passed++
Write-Output 'PASS fixture: PowerShell, production pin, and current-engine static smoke check'

$tick = [string][char]0x60
$tilde = [string][char]0x7e
$formFeed = [string][char]0x0c
$normalizedPageProbe = Convert-ToNoticeLegalText ("page one`tvalue`r`n" + $formFeed + "page two`r")
if ($normalizedPageProbe -cne "page one`tvalue`n`npage two`n" -or $normalizedPageProbe.IndexOf($formFeed, [StringComparison]::Ordinal) -ge 0) {
    throw 'notice legal text did not normalize CR and form feed deterministically'
}
$tiePayload = "prefix`n$($tick * 3)`n$($tilde * 3)`nsuffix`n"
if ((Get-NoticeLegalFence $tiePayload) -cne ($tick * 4)) { throw 'notice legal fence tie must select four backticks' }
$tildePayload = "prefix`n$($tick * 4)`n$($tilde * 3)`nsuffix`n"
if ((Get-NoticeLegalFence $tildePayload) -cne ($tilde * 4)) { throw 'notice legal fence must select the shorter tilde candidate' }
$backtickPayload = "prefix`n$($tick * 3)`n$($tilde * 4)`nsuffix`n"
if ((Get-NoticeLegalFence $backtickPayload) -cne ($tick * 4)) { throw 'notice legal fence must select the shorter backtick candidate' }
$deterministicFenceA = Get-NoticeLegalFence $normalizedPageProbe
$deterministicFenceB = Get-NoticeLegalFence $normalizedPageProbe
if ($deterministicFenceA -cne $deterministicFenceB) { throw 'notice legal fence selection is not deterministic' }

$docsProbeRoot = Join-Path ([IO.Path]::GetTempPath()) ($fixtureNamePrefix + 'docs-fence-' + [guid]::NewGuid().ToString('N'))
try {
    New-Item -ItemType Directory -Path $docsProbeRoot | Out-Null
    $probePayload = Convert-ToNoticeLegalText ("payload [hidden](/inside-fence)`n$($tick * 3)")
    $probeLines = New-Object System.Collections.Generic.List[string]
    $probeLines.Add('# Fence probe')
    $probeLines.Add('')
    Add-NoticeLegalPayload $probeLines $probePayload
    $probeLines.Add('[visible](/after-fence)')
    Write-Utf8Canonical (Join-Path $docsProbeRoot 'probe.md') (@($probeLines) -join "`n")
    $docsProbeResult = Invoke-DocsProbeProcess $docsProbeRoot
    if ($docsProbeResult.TimedOut -or $docsProbeResult.ExitCode -eq 0) { throw 'docs fence-closure probe did not fail on visible bad content' }
    if ($docsProbeResult.Stderr -notmatch '(?m)^DOC_LINK_ABSOLUTE path=probe\.md line=7\r?$') {
        throw 'docs fence-closure probe did not detect the bad link after the generated closing fence'
    }
    if ($docsProbeResult.Stderr -match '(?m)^DOC_LINK_ABSOLUTE path=probe\.md line=4\r?$') {
        throw 'docs fence-closure probe treated fenced legal payload as visible Markdown'
    }
} finally {
    Remove-Fixture $docsProbeRoot
}
$script:passed++
Write-Output 'PASS fixture: normalized legal payload uses deterministic collision-safe fences visible to Docs'

Invoke-Case 'valid exact closure accepts CRLF and missing-final-LF legal sources' $null @()

Invoke-Case 'stale dependency-manifest pin fails closed' {
    param($f)
    $text = [IO.File]::ReadAllText($f.DependencyPath, [Text.Encoding]::UTF8).TrimEnd([char]10)
    [IO.File]::WriteAllText($f.DependencyPath, $text + " `n", [Text.UTF8Encoding]::new($false))
} @('LICENSE_DEPENDENCY_MANIFEST_PIN_MISMATCH')

try {
    $env:FREEAGENT_DEPENDENCY_MANIFEST_SHA256_OVERRIDE = 'f' * 64
    Invoke-Case 'dependency-manifest pin ignores environment overrides' {
        param($f)
        $text = [IO.File]::ReadAllText($f.DependencyPath, [Text.Encoding]::UTF8).TrimEnd([char]10)
        [IO.File]::WriteAllText($f.DependencyPath, $text + " `n", [Text.UTF8Encoding]::new($false))
    } @('LICENSE_DEPENDENCY_MANIFEST_PIN_MISMATCH')
} finally {
    Remove-Item Env:FREEAGENT_DEPENDENCY_MANIFEST_SHA256_OVERRIDE -ErrorAction SilentlyContinue
}

Invoke-Case 'exact compatibility policy rejects target drift' {
    param($f)
    $f.DependencyManifest.compatibility_policy.binary_targets = @('linux/amd64')
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_COMPATIBILITY_INVALID')

Invoke-Case 'module compatibility conclusion rejects broader status' {
    param($f)
    $f.DependencyManifest.modules[0].compatibility_conclusion.status = 'reviewed-compatible'
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_COMPATIBILITY_INVALID')

Invoke-Case 'declared SPDX accepts canonical sorted flat AND atoms' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'Apache-2.0 AND CC-BY-4.0 AND MIT'
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'declared GPL atom is allowed only under the exact scoped policy' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'GPL-2.0-only AND MIT'
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'declared SPDX rejects non-Ordinal order' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'MIT AND Apache-2.0'
    Write-ManifestsAndNotice $f
} @('LICENSE_DECLARED_SPDX_NONCANONICAL')

Invoke-Case 'declared SPDX rejects duplicate atoms' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'MIT AND MIT'
    Write-ManifestsAndNotice $f
} @('LICENSE_DECLARED_SPDX_NONCANONICAL')

Invoke-Case 'declared SPDX rejects noncanonical whitespace' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'Apache-2.0  AND MIT'
    Write-ManifestsAndNotice $f
} @('LICENSE_UNKNOWN_SPDX')

Invoke-Case 'declared SPDX rejects OR and parentheses' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = '(Apache-2.0 OR MIT)'
    Write-ManifestsAndNotice $f
} @('LICENSE_UNKNOWN_SPDX')

Invoke-Case 'identity-scoped LicenseRef binds exact legal file and hash' {
    param($f)
    $license = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'LICENSE' })[0]
    $id = 'LicenseRef-example-dependency-terms'
    $f.DependencyManifest.license_refs = @([ordered]@{
        id = $id
        name = 'Example dependency terms'
        source_url = 'https://example.invalid/module-license'
        text_sha256 = [string]$license.sha256
        applies_to = 'example.org/dep@v1.2.3'
        required_file = 'LICENSE'
    })
    $f.DependencyManifest.modules[0].declared_license_expression = $id
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'LicenseRef cannot be orphaned from declared expression' {
    param($f)
    $license = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'LICENSE' })[0]
    $f.DependencyManifest.license_refs = @([ordered]@{
        id = 'LicenseRef-orphan'
        name = 'Orphan terms'
        source_url = 'https://example.invalid/orphan'
        text_sha256 = [string]$license.sha256
        applies_to = 'example.org/dep@v1.2.3'
        required_file = 'LICENSE'
    })
    Write-ManifestsAndNotice $f
} @('LICENSE_LICENSE_REF_UNUSED')

Invoke-Case 'LicenseRef rejects cross-module identity scope' {
    param($f)
    $license = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'LICENSE' })[0]
    $id = 'LicenseRef-cross-scope'
    $f.DependencyManifest.license_refs = @([ordered]@{
        id = $id
        name = 'Cross scope terms'
        source_url = 'https://example.invalid/cross-scope'
        text_sha256 = [string]$license.sha256
        applies_to = 'example.org/other@v1.0.0'
        required_file = 'LICENSE'
    })
    $f.DependencyManifest.modules[0].declared_license_expression = $id
    Write-ManifestsAndNotice $f
} @('LICENSE_LICENSE_REF_SCOPE_INVALID')

Invoke-Case 'LicenseRef rejects required-file hash drift' {
    param($f)
    $id = 'LicenseRef-wrong-hash'
    $f.DependencyManifest.license_refs = @([ordered]@{
        id = $id
        name = 'Wrong hash terms'
        source_url = 'https://example.invalid/wrong-hash'
        text_sha256 = 'f' * 64
        applies_to = 'example.org/dep@v1.2.3'
        required_file = 'LICENSE'
    })
    $f.DependencyManifest.modules[0].declared_license_expression = $id
    Write-ManifestsAndNotice $f
} @('LICENSE_LICENSE_REF_BINDING_INVALID')

Invoke-Case 'source SPDX marker addition invalidates attestation' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'added.txt') '// SPDX-License-Identifier: BSD-3-Clause'
} @('LICENSE_DEPENDENCY_SPDX_SCAN_MISMATCH')

Invoke-Case 'source SPDX marker removal invalidates attestation' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') 'marker removed'
} @('LICENSE_DEPENDENCY_SPDX_SCAN_MISMATCH')

Invoke-Case 'source SPDX marker movement changes digest with same set and count' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') "blank`n// SPDX-License-Identifier: MIT"
} @('LICENSE_DEPENDENCY_SPDX_SCAN_MISMATCH')

Invoke-Case 'source SPDX repeated marker changes occurrence count' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') "// SPDX-License-Identifier: MIT`n// SPDX-License-Identifier: MIT"
} @('LICENSE_DEPENDENCY_SPDX_SCAN_MISMATCH')

Invoke-Case 'hidden no-ignore source marker is scanned' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory '.hidden/marker.txt') '// SPDX-License-Identifier: BSD-2-Clause'
    Sync-DependencySPDXScan $f
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'detected WITH expression is accepted exactly' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') '// SPDX-License-Identifier: Apache-2.0 WITH LLVM-exception'
    Sync-DependencySPDXScan $f
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'detected parenthesized expression is accepted exactly' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') '// SPDX-License-Identifier: (BSD-3-Clause AND ISC)'
    Sync-DependencySPDXScan $f
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'same-line marker occurrence order is deterministic' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') "// SPDX-License-Identifier: BSD-3-Clause`tSPDX-License-Identifier: MIT"
    Sync-DependencySPDXScan $f
    if ([int64]$f.DependencyManifest.modules[0].source_spdx_scan.occurrence_count -ne 2) { throw 'same-line marker occurrences were not counted independently' }
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'unknown detected SPDX expression fails closed' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') '// SPDX-License-Identifier: LicenseRef-Unknown'
} @('LICENSE_DEPENDENCY_SPDX_UNKNOWN')

Invoke-Case 'detected SPDX internal whitespace must be canonical' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'source.txt') '// SPDX-License-Identifier: Apache-2.0  WITH LLVM-exception'
} @('LICENSE_DEPENDENCY_SPDX_UNKNOWN')

Invoke-Case 'marker-bearing non-UTF8 archive file fails closed' {
    param($f)
    $prefix = [Text.Encoding]::ASCII.GetBytes('SPDX-License-Identifier: MIT ')
    $bytes = New-Object byte[] ($prefix.Length + 1)
    [Array]::Copy($prefix, $bytes, $prefix.Length)
    $bytes[$bytes.Length - 1] = 0xff
    [IO.File]::WriteAllBytes((Join-Path $f.DependencyDirectory 'invalid.bin'), $bytes)
} @('LICENSE_DEPENDENCY_SPDX_UTF8_INVALID')

Invoke-Case 'non-UTF8 archive file without marker remains scannable' {
    param($f)
    [IO.File]::WriteAllBytes((Join-Path $f.DependencyDirectory 'opaque.bin'), [byte[]](0xff, 0xfe, 0xfd))
} @()

Invoke-Case 'archive scan golden digest locks path line and same-line ordering' {
    param($f)
    Remove-Item -LiteralPath (Join-Path $f.DependencyDirectory 'source.txt') -Force
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory '.hidden/a.txt') 'SPDX-License-Identifier: MIT'
    $zLines = @('blank', "SPDX-License-Identifier: BSD-3-Clause`tSPDX-License-Identifier: MIT") + @('blank') * 7 + @('SPDX-License-Identifier: Apache-2.0 WITH LLVM-exception')
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'z.txt') ($zLines -join "`n")
    Sync-DependencySPDXScan $f
    if ([string]$f.DependencyManifest.modules[0].source_spdx_scan.sha256 -cne '6e4ff3c9d51c286c2ed6943d099c8c0c9aa1195c385195b3e8b5df03f0aec98f') {
        throw 'archive SPDX golden digest changed'
    }
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'Go module graph rejects an empty JSON object stream' {
    param($f)
    $f.GoCommand = New-FakeGoModuleStream $f.Root ''
} @('LICENSE_GO_GRAPH_INVALID')

Invoke-Case 'Go module graph rejects a non-object top-level JSON value' {
    param($f)
    $f.GoCommand = New-FakeGoModuleStream $f.Root '[]'
} @('LICENSE_GO_GRAPH_INVALID')

Invoke-Case 'Go module graph rejects trailing non-JSON content' {
    param($f)
    $f.GoCommand = New-FakeGoModuleStream $f.Root '{"Path":"github.com/endview/freeagent","Main":true} trailing'
} @('LICENSE_GO_GRAPH_INVALID')

Invoke-Case 'Go module graph rejects duplicate top-level object keys' {
    param($f)
    $f.GoCommand = New-FakeGoModuleStream $f.Root '{"Path":"github.com/endview/freeagent","Path":"example.invalid/duplicate","Main":true}'
} @('LICENSE_GO_GRAPH_INVALID')

Invoke-Case 'Go module graph rejects duplicate nested object keys' {
    param($f)
    $f.GoCommand = New-FakeGoModuleStream $f.Root '{"Path":"github.com/endview/freeagent","Main":true,"Error":{"Err":"first","Err":"second"}}'
} @('LICENSE_GO_GRAPH_INVALID')

Invoke-Case 'go mod verify pre-scan failure is fail-closed and redacted' {
    param($f)
    $absoluteLeak = if ($testIsWindows) { 'Q' + [char]0x3a + [char]0x5c + 'private' + [char]0x5c + 'module-cache' } else { [string][char]0x2f + 'private/module-cache' }
    $f.SensitivePaths += $absoluteLeak
    $f.GoCommand = New-FakeGoVerifyPolicy $f.Root $f.DependencyDirectory 'pre-fail' '' $absoluteLeak
} @('LICENSE_GO_VERIFY_BEFORE_FAILED')

Invoke-Case 'go mod verify post-scan failure proves the second verification runs' {
    param($f)
    $f.GoCommand = New-FakeGoVerifyPolicy $f.Root $f.DependencyDirectory 'post-fail'
} @('LICENSE_GO_VERIFY_AFTER_FAILED')

Invoke-Case 'module-cache tamper after pre-verify is caught by post-verify' {
    param($f)
    $sentinel = Join-Path $f.DependencyDirectory 'opaque-payload.bin'
    [IO.File]::WriteAllBytes($sentinel, [byte[]](1, 2, 3, 4))
    $f.GoCommand = New-FakeGoVerifyPolicy $f.Root $f.DependencyDirectory 'tamper-after-pre' $sentinel
} @('LICENSE_GO_VERIFY_AFTER_FAILED')

Invoke-Case 'go mod verify cannot mutate go.mod' {
    param($f)
    $f.GoCommand = New-FakeGoVerifyPolicy $f.Root $f.DependencyDirectory 'mutate-go-mod'
} @('LICENSE_GO_SOURCE_MUTATED')

Invoke-Case 'dependency legal aliases remain in the exact legal bundle' {
    param($f)
    $additions = @(
        @{ Path = 'AUTHORS'; Role = 'attribution' },
        @{ Path = 'COPYING'; Role = 'license' },
        @{ Path = 'COPYRIGHT'; Role = 'copyright' },
        @{ Path = 'CREDITS'; Role = 'attribution' },
        @{ Path = 'CONTRIBUTORS'; Role = 'attribution' },
        @{ Path = 'GO-LICENSE'; Role = 'license' },
        @{ Path = 'LICENSE-3RD-PARTY.md'; Role = 'license' },
        @{ Path = 'LICENSE-GO'; Role = 'license' },
        @{ Path = 'LICENSE-LOGO'; Role = 'license' },
        @{ Path = 'LICENSE-MIT'; Role = 'license' },
        @{ Path = 'LICENSE-MMAP-GO'; Role = 'license' },
        @{ Path = 'LICENSE.txt'; Role = 'license' },
        @{ Path = 'SQLITE-LICENSE'; Role = 'license' }
    )
    $records = @($f.DependencyManifest.modules[0].required_files)
    foreach ($addition in $additions) {
        $fullPath = Join-Path $f.DependencyDirectory $addition.Path
        Write-Utf8Canonical $fullPath ("Example dependency legal text for " + $addition.Path)
        $records += New-RequiredFileRecord $addition.Path $addition.Role (Get-Item -LiteralPath $fullPath)
    }
    $paths = [string[]]@($records | ForEach-Object { [string]$_.path })
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $orderedRecords = @()
    foreach ($path in $paths) {
        $orderedRecords += @($records | Where-Object { [string]$_.path -ceq $path })[0]
    }
    $f.DependencyManifest.modules[0].required_files = $orderedRecords
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'ordinary module directory named copyright is traversed but is not a legal file' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'copyright/source.txt') 'ordinary source directory content'
} @()

Invoke-Case 'nested module legal basename is part of the exact closure' {
    param($f)
    $relative = 'third_party/svgpan/LICENSE'
    $fullPath = Join-Path $f.DependencyDirectory $relative
    Write-Utf8Canonical $fullPath 'Nested component license'
    $records = @($f.DependencyManifest.modules[0].required_files) + @(New-RequiredFileRecord $relative 'license' (Get-Item -LiteralPath $fullPath))
    $paths = [string[]]@($records | ForEach-Object { [string]$_.path })
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $ordered = @()
    foreach ($path in $paths) { $ordered += @($records | Where-Object { [string]$_.path -ceq $path })[0] }
    $f.DependencyManifest.modules[0].required_files = $ordered
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'nested module legal basename cannot be omitted' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'third_party/svgpan/LICENSE') 'Nested component license'
} @('LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH')

Invoke-Case 'dependency required files must remain Ordinal sorted' {
    param($f)
    $records = @($f.DependencyManifest.modules[0].required_files)
    $f.DependencyManifest.modules[0].required_files = @($records[1], $records[0]) + @($records | Select-Object -Skip 2)
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_ORDER_INVALID')

Invoke-Case 'legal-prefix source filenames are not legal documents' {
    param($f)
    foreach ($name in @('copyright_test.go', 'LICENSE_test.go', 'NOTICE_helper.ts', 'COPYING.c', 'PATENTS.rs')) {
        $content = if ($name.EndsWith('.go', [StringComparison]::Ordinal)) { 'package ignored' } else { 'source-like test fixture' }
        Write-Utf8Canonical (Join-Path $f.DependencyDirectory $name) $content
    }
} @()

Invoke-Case 'Current Store migration root is part of the distributed asset closure' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/currentstore/migrations/9999_fixture.sql') 'SELECT 1;'
} @('LICENSE_ASSET_SET_MISMATCH')

Invoke-Case 'main-module-only with audited empty distributed assets' {
    param($f)
    foreach ($relative in @(
        'examples/project.json',
        'testdata/external.txt',
        'third_party/example/LICENSE',
        'third_party/example/LICENSES/MIT.txt',
        'third_party/example/NOTICE',
        'third_party/example/PATENTS',
        'third_party/example/tool.go'
    )) {
        Remove-Item -LiteralPath (Join-Path $f.Root $relative) -Force
    }
    Write-Utf8Canonical (Join-Path $f.Root 'go.mod') "module github.com/endview/freeagent`n`ngo 1.26.5"
    Write-Utf8Canonical (Join-Path $f.Root 'go.sum') ''
    $f.DependencyManifest.modules = @()
    $f.AssetManifest.assets = @()
    $f.GoCommand = New-FakeGoMainOnly $f.Root
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'root LICENSE byte mismatch' {
    param($f)
    [IO.File]::AppendAllText((Join-Path $f.Root 'LICENSE'), 'x', [Text.UTF8Encoding]::new($false))
} @('LICENSE_ROOT_HASH_MISMATCH')

Invoke-Case 'root alternate license filename rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'LICENSE-MIT.txt') 'conflicting alternate license'
} @('LICENSE_ROOT_ALTERNATE_FORBIDDEN')

Invoke-Case 'conflicting project declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') '# Fixture AGPL-3.0-or-later [LICENSE](LICENSE)'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'conflicting project SPDX header' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'CONTRIBUTING.md') "Contributions use AGPL-3.0-only.`nSPDX-License-Identifier: MIT"
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'README split Markdown SPDX header is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`nSPDX-License-`nIdentifier: MIT"
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'CONTRIBUTING split Markdown SPDX header is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'CONTRIBUTING.md') "Contributions use ``AGPL-3.0-only``.`nSPDX-License-`nIdentifier: MIT"
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'PRD split Markdown SPDX header is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'docs/PRD.md') "License: ``AGPL-3.0-only```nSPDX-License-`nIdentifier: MIT"
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'single-line matching Markdown SPDX header remains valid' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'CONTRIBUTING.md') "Contributions use ``AGPL-3.0-only``.`nSPDX-License-Identifier: AGPL-3.0-only"
} @()

Invoke-Case 'explicitly negated AGPL declaration is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') '# Fixture`n`nThis project is not licensed under AGPL-3.0-only; see [LICENSE](LICENSE).'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'English rejection of AGPL declaration is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') '# Fixture`n`nThis project rejects the AGPL-3.0-only license; see [LICENSE](LICENSE).'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'English after-token license negation is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') 'AGPL-3.0-only does not apply to this project; see [LICENSE](LICENSE).'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'English not-governed statement is not affirmative' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') 'This project is not governed by AGPL-3.0-only; see [LICENSE](LICENSE).'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'Chinese negation of AGPL declaration is rejected' {
    param($f)
    $reject = [string][char]0x62d2 + [char]0x7edd
    $license = [string][char]0x8bb8 + [char]0x53ef
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("# Fixture`n`n$reject$license AGPL-3.0-only; [LICENSE](LICENSE).")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'Chinese did-not-adopt statement is not affirmative' {
    param($f)
    $project = -join @([char]0x672c, [char]0x9879, [char]0x76ee)
    $didNotAdopt = -join @([char]0x6ca1, [char]0x6709, [char]0x91c7, [char]0x7528)
    $licenseCertificate = -join @([char]0x8bb8, [char]0x53ef, [char]0x8bc1)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("$project$didNotAdopt AGPL-3.0-only $licenseCertificate; [LICENSE](LICENSE).")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'canonical declaration inside a fenced code block is not prose' {
    param($f)
    $text = @'
# Fixture

```text
Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
```
'@
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') $text
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'HTML-comment text in fence info does not open a comment' {
    param($f)
    $text = @'
~~~ <!-- literal fence info
Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
~~~
Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
'@
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') $text
} @()

Invoke-Case 'invalid backtick-fence info string does not hide a contradiction' {
    param($f)
    $text = @'
Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
```bad`info
This project is not governed by AGPL-3.0-only.
'@
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') $text
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'unmatched backtick cannot suppress a following fenced declaration' {
    param($f)
    $tick = [string][char]0x60
    $fence = $tick + $tick + $tick
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("${tick}unmatched`n${fence}text`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).`n$fence")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'canonical declaration inside an indented code block is not prose' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "# Fixture`n`n    Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n`n`tLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'indented declaration after a heading remains a code block' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "# Fixture`n    Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'HTML-comment opener inside indented code does not hide later prose' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "# Fixture`n    <!-- literal code`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @()

Invoke-Case 'canonical declaration inside an HTML comment is not prose' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "# Fixture`n`n<!--`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n-->"
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'unmatched backtick cannot suppress a following block HTML comment' {
    param($f)
    $tick = [string][char]0x60
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("${tick}unmatched`n<!--`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).`n-->")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'unmatched backtick cannot suppress a same-line HTML comment' {
    param($f)
    $tick = [string][char]0x60
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("visible ${tick}unmatched <!--`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).`n-->")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'escaped backtick cannot suppress an HTML comment' {
    param($f)
    $tick = [string][char]0x60
    $slash = [string][char]0x5c
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("$slash${tick}<!--`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).`n-->")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'inline code containing an HTML-comment opener does not hide later prose' {
    param($f)
    $tick = [string][char]0x60
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("$tick<!--$tick`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).")
} @()

Invoke-Case 'multiline code span containing an HTML-comment opener does not start a comment' {
    param($f)
    $tick = [string][char]0x60
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("${tick}literal`n<!-- remains code`nspan${tick}`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).")
} @()

Invoke-Case 'canonical declaration inside a multiline double-backtick code span is not prose' {
    param($f)
    $tick = [string][char]0x60
    $delimiter = $tick + $tick
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("# Fixture`n`n${delimiter}example`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).`nexample${delimiter}")
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'multiline double-backtick code span does not hide later declaration prose' {
    param($f)
    $tick = [string][char]0x60
    $delimiter = $tick + $tick
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("${delimiter}example`nnot declaration prose`nexample${delimiter}`nLicensed as ${tick}AGPL-3.0-only${tick}; see [LICENSE](LICENSE).")
} @()

Invoke-Case 'paragraph continuation indentation remains visible prose' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Fixture paragraph`n    Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @()

Invoke-Case 'English affirmative and negative AGPL declarations conflict' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`nThis project is not governed by`nAGPL-3.0-only."
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'Markdown-emphasized AGPL contradiction is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`nThis project is not governed by **AGPL-3.0-only**."
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'English emphasized soft-wrapped negation is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`nThis project is **not governed`nby** AGPL-3.0-only."
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'Chinese emphasized soft-wrapped negation is rejected' {
    param($f)
    $project = -join @([char]0x672c, [char]0x9879, [char]0x76ee)
    $didNotAdopt = -join @([char]0x6ca1, [char]0x6709, [char]0x91c7, [char]0x7528)
    $licenseCertificate = -join @([char]0x8bb8, [char]0x53ef, [char]0x8bc1)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n$project**$didNotAdopt`n** AGPL-3.0-only $licenseCertificate.")
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'emphasized soft-wrapped alternate-license negation remains valid' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`nThis project is **not governed`nby** MIT."
} @()

Invoke-Case 'Chinese affirmative and negative AGPL declarations conflict' {
    param($f)
    $project = -join @([char]0x672c, [char]0x9879, [char]0x76ee)
    $didNotAdopt = -join @([char]0x6ca1, [char]0x6709, [char]0x91c7, [char]0x7528)
    $licenseCertificate = -join @([char]0x8bb8, [char]0x53ef, [char]0x8bc1)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n$project$didNotAdopt`nAGPL-3.0-only $licenseCertificate.")
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'quoted negative AGPL example does not conflict with prose declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n> This project is not governed by AGPL-3.0-only."
} @()

Invoke-Case 'lazy blockquote continuation cannot supply a canonical declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "> quoted introduction`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'nested lazy blockquote continuation cannot supply a canonical declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "> > > quoted introduction`nLicensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE)."
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'lazy blockquote negative example does not conflict with prose declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n> quoted introduction`nThis project is not governed by AGPL-3.0-only."
} @()

Invoke-Case 'nested lazy blockquote negative example does not conflict with prose declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n`n> > quoted introduction`nThis project is not governed by AGPL-3.0-only."
} @()

Invoke-Case 'SPDX compatibility note is not a project-license declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') 'SPDX compatibility note: AGPL-3.0-only; see [LICENSE](LICENSE).'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'Chinese project-license conflict is rejected' {
    param($f)
    $licenseCertificate = [string][char]0x8bb8 + [char]0x53ef + [char]0x8bc1
    Write-Utf8Canonical (Join-Path $f.Root 'CONTRIBUTING.md') ("Contributions use AGPL-3.0-only.`n${licenseCertificate}: MIT")
} @('LICENSE_DECLARATION_CONFLICT')

Invoke-Case 'unrelated English negation does not reject an AGPL declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "No warranty.`nThis project is licensed under AGPL-3.0-only; see [LICENSE](LICENSE)."
} @()

Invoke-Case 'negated alternate English license does not conflict with AGPL' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "This project is licensed under AGPL-3.0-only; see [LICENSE](LICENSE).`nThis project is not licensed under MIT."
} @()

Invoke-Case 'same-paragraph alternate-license negation does not scope over AGPL' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') "This project is licensed under AGPL-3.0-only; see [LICENSE](LICENSE).`nThis project is not licensed under MIT, but AGPL-3.0-only remains the project license."
} @()

Invoke-Case 'unrelated Chinese negation and negated alternate license remain valid' {
    param($f)
    $noWarranty = -join @([char]0x4e0d, [char]0x63d0, [char]0x4f9b, [char]0x4efb, [char]0x4f55, [char]0x62c5, [char]0x4fdd)
    $projectUses = -join @([char]0x672c, [char]0x9879, [char]0x76ee, [char]0x91c7, [char]0x7528)
    $licenseCertificate = -join @([char]0x8bb8, [char]0x53ef, [char]0x8bc1)
    $details = -join @([char]0x8be6, [char]0x89c1)
    $fullStop = [string][char]0x3002
    $semicolon = [string][char]0xff1b
    $ratherThan = -join @([char]0x800c, [char]0x4e0d, [char]0x662f)
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("$projectUses AGPL-3.0-only $licenseCertificate$semicolon$details [LICENSE](LICENSE)$fullStop`n$noWarranty; $ratherThan MIT.")
} @()

Invoke-Case 'same-paragraph Chinese alternate-license negation does not scope over AGPL' {
    param($f)
    $project = -join @([char]0x672c, [char]0x9879, [char]0x76ee)
    $didNotAdopt = -join @([char]0x6ca1, [char]0x6709, [char]0x91c7, [char]0x7528)
    $insteadUses = -join @([char]0x800c, [char]0x91c7, [char]0x7528)
    $comma = [string][char]0xff0c
    $fullStop = [string][char]0x3002
    Write-Utf8Canonical (Join-Path $f.Root 'README.md') ("Licensed as ``AGPL-3.0-only``; see [LICENSE](LICENSE).`n$project$didNotAdopt MIT$comma$insteadUses AGPL-3.0-only$fullStop")
} @()

Invoke-Case 'bare AGPL token is not an affirmative declaration' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'CONTRIBUTING.md') 'AGPL-3.0-only'
} @('LICENSE_DECLARATION_MISSING')

Invoke-Case 'all R0-2 products missing stays transitional red' {
    param($f)
    Remove-Item -LiteralPath $f.DependencyPath -Force
    Remove-Item -LiteralPath $f.AssetPath -Force
    Remove-Item -LiteralPath $f.NoticesPath -Force
} @('LICENSE_DEPENDENCY_MANIFEST_MISSING', 'LICENSE_ASSET_MANIFEST_MISSING', 'LICENSE_NOTICE_MISSING')

Invoke-Case 'strict JSON duplicate property' {
    param($f)
    $text = [IO.File]::ReadAllText($f.DependencyPath, [Text.Encoding]::UTF8)
    $text = $text.Substring(0, $text.IndexOf('{') + 1) + '"schema_version":1,' + $text.Substring($text.IndexOf('{') + 1)
    Write-Utf8Canonical $f.DependencyPath $text
} @('LICENSE_DEPENDENCY_JSON_INVALID')

Invoke-Case 'strict schema rejects coerced string version' {
    param($f)
    $f.DependencyManifest.schema_version = '1'
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_SCHEMA_INVALID')

Invoke-Case 'HTTPS source rejects userinfo' {
    param($f)
    $scheme = 'ht' + 'tps' + '://'
    $f.DependencyManifest.modules[0].source_url = $scheme + 'user' + [char]0x40 + 'example.invalid/module'
    Write-DependencyManifestAndPin $f
} @('LICENSE_DEPENDENCY_SCHEMA_INVALID')

Invoke-Case 'manifest string rejects control character' {
    param($f)
    $f.DependencyManifest.modules[0].source_url = "https://example.invalid/`tmodule"
    Write-DependencyManifestAndPin $f
} @('LICENSE_DEPENDENCY_SCHEMA_INVALID')

Invoke-Case 'dependency required path rejects Markdown table injection' {
    param($f)
    $licenseRecord = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'LICENSE' })[0]
    $licenseRecord.path = 'LICENSE|NOTICE'
    Write-DependencyManifestAndPin $f
} @('LICENSE_DEPENDENCY_SCHEMA_INVALID')

Invoke-Case 'selected graph missing manifest module' {
    param($f)
    $f.DependencyManifest.modules = @()
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_GRAPH_MISMATCH')

Invoke-Case 'extra dependency entry' {
    param($f)
    $extra = [ordered]@{}
    foreach ($key in $f.DependencyManifest.modules[0].Keys) { $extra[$key] = $f.DependencyManifest.modules[0][$key] }
    $extra.path = 'example.org/extra'
    $extra.notice_id = Get-NoticeID 'go' 'example.org/extra@v1.2.3'
    $f.DependencyManifest.modules = @($f.DependencyManifest.modules[0], $extra)
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_GRAPH_MISMATCH')

Invoke-Case 'duplicate dependency entry' {
    param($f)
    $f.DependencyManifest.modules = @($f.DependencyManifest.modules[0], $f.DependencyManifest.modules[0])
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_DUPLICATE')

Invoke-Case 'dependency license hash mismatch' {
    param($f)
    $licenseRecord = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'LICENSE' })[0]
    $licenseRecord.sha256 = '0' * 64
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_FILE_HASH_MISMATCH')

Invoke-Case 'dependency legal bundle cannot omit root NOTICE' {
    param($f)
    $f.DependencyManifest.modules[0].required_files = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -cne 'NOTICE' })
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH')

Invoke-Case 'dependency legal bundle cannot omit PATENTS' {
    param($f)
    $f.DependencyManifest.modules[0].required_files = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -cne 'PATENTS' })
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH')

Invoke-Case 'dependency legal bundle cannot omit nested LICENSES file' {
    param($f)
    $f.DependencyManifest.modules[0].required_files = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -cne 'LICENSES/Apache-2.0.txt' })
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH')

Invoke-Case 'dependency nested license directory name is canonical' {
    param($f)
    $canonical = Join-Path $f.DependencyDirectory 'LICENSES'
    $intermediate = Join-Path $f.DependencyDirectory 'LICENSES-temp'
    Rename-Item -LiteralPath $canonical -NewName 'LICENSES-temp'
    Rename-Item -LiteralPath $intermediate -NewName 'licenses'
} @('LICENSE_DEPENDENCY_LEGAL_BUNDLE_INVALID')

Invoke-Case 'dependency cannot disguise go.mod as a license file' {
    param($f)
    $goMod = Get-Item -LiteralPath (Join-Path $f.DependencyDirectory 'go.mod')
    $f.DependencyManifest.modules[0].required_files += New-RequiredFileRecord 'go.mod' 'license' $goMod
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH')

Invoke-Case 'dependency legal role is derived from basename' {
    param($f)
    $noticeRecord = @($f.DependencyManifest.modules[0].required_files | Where-Object { [string]$_.path -ceq 'NOTICE' })[0]
    $noticeRecord.role = 'license'
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_LEGAL_ROLE_MISMATCH')

Invoke-Case 'blank dependency legal body is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'LICENSES/Apache-2.0.txt') ([string][char]0x200b)
    Sync-DependencyFileMetadata $f 'LICENSES/Apache-2.0.txt'
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_TEXT_INVALID')

Invoke-Case 'supplementary format-only dependency legal body is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'LICENSES/Apache-2.0.txt') ([char]::ConvertFromUtf32(0xE0001))
    Sync-DependencyFileMetadata $f 'LICENSES/Apache-2.0.txt'
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_TEXT_INVALID')

Invoke-Case 'supplementary letter is meaningful dependency legal text' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'LICENSES/Apache-2.0.txt') ([char]::ConvertFromUtf32(0x10400))
    Sync-DependencyFileMetadata $f 'LICENSES/Apache-2.0.txt'
    Write-ManifestsAndNotice $f
} @()

Invoke-Case 'form feed and CR normalize before deterministic fenced notice rendering' {
    param($f)
    $formFeed = [string][char]0x0c
    $rawText = "Page one`tcolumn`r`n" + $formFeed + "Page two`r"
    Write-Utf8Raw (Join-Path $f.DependencyDirectory 'LICENSE') $rawText
    Sync-DependencyFileMetadata $f 'LICENSE'
    Write-ManifestsAndNotice $f
    $notices = [IO.File]::ReadAllText($f.NoticesPath, [Text.Encoding]::UTF8)
    if ($notices.IndexOf($formFeed, [StringComparison]::Ordinal) -ge 0) { throw 'rendered notices retained a form-feed character' }
    $normalized = Convert-ToNoticeLegalText $rawText
    $fence = Get-NoticeLegalFence $normalized
    $expectedPayload = $fence + "`n" + $normalized.Substring(0, $normalized.Length - 1) + "`n" + $fence
    if ($notices.IndexOf($expectedPayload, [StringComparison]::Ordinal) -lt 0) { throw 'rendered notices did not preserve the normalized page break inside a fence' }
} @()

Invoke-Case 'notice renderer survives backtick and tilde closing-like collisions' {
    param($f)
    $tick = [string][char]0x60
    $tilde = [string][char]0x7e
    $dependencyText = "Dependency payload`n$($tick * 3)`n$($tilde * 3)`nend"
    $assetText = "Asset payload`n$($tick * 4)`n$($tilde * 3)`nend"
    Write-Utf8Raw (Join-Path $f.DependencyDirectory 'LICENSE') $dependencyText
    Sync-DependencyFileMetadata $f 'LICENSE'
    Write-Utf8Raw (Join-Path $f.Root 'third_party/example/LICENSE') $assetText
    Sync-AssetFileMetadata $f 'third_party/example/LICENSE'
    Write-ManifestsAndNotice $f
    $notices = [IO.File]::ReadAllText($f.NoticesPath, [Text.Encoding]::UTF8)
    $normalizedDependency = Convert-ToNoticeLegalText $dependencyText
    $dependencyFence = $tick * 4
    $expectedDependency = $dependencyFence + "`n" + $normalizedDependency.Substring(0, $normalizedDependency.Length - 1) + "`n" + $dependencyFence
    if ($notices.IndexOf($expectedDependency, [StringComparison]::Ordinal) -lt 0) { throw 'backtick tie fence did not contain both N-1 closing-like runs' }
    $normalizedAsset = Convert-ToNoticeLegalText $assetText
    $assetFence = $tilde * 4
    $expectedAsset = $assetFence + "`n" + $normalizedAsset.Substring(0, $normalizedAsset.Length - 1) + "`n" + $assetFence
    if ($notices.IndexOf($expectedAsset, [StringComparison]::Ordinal) -lt 0) { throw 'shorter tilde fence did not contain both closing-like runs' }
    $firstRender = New-ExpectedNotices $f.DependencyManifest $f.AssetManifest $f.DependencyPath $f.AssetPath $f.DependencyDirectory $f.Root
    $secondRender = New-ExpectedNotices $f.DependencyManifest $f.AssetManifest $f.DependencyPath $f.AssetPath $f.DependencyDirectory $f.Root
    if ($firstRender -cne $secondRender) { throw 'identical legal inputs produced nondeterministic notices' }
} @()

Invoke-Case 'NUL in dependency legal payload fails closed before notice comparison' {
    param($f)
    Write-Utf8Raw (Join-Path $f.DependencyDirectory 'LICENSE') ('valid' + [string][char]0x00 + 'payload')
    Sync-DependencyFileMetadata $f 'LICENSE'
    Write-DependencyManifestAndPin $f
} @('LICENSE_NOTICE_RENDER_INVALID')

Invoke-Case 'DEL in dependency legal payload fails closed before notice comparison' {
    param($f)
    Write-Utf8Raw (Join-Path $f.DependencyDirectory 'NOTICE') ('valid' + [string][char]0x7f + 'payload')
    Sync-DependencyFileMetadata $f 'NOTICE'
    Write-DependencyManifestAndPin $f
} @('LICENSE_NOTICE_RENDER_INVALID')

Invoke-Case 'C1 control in asset legal payload fails closed before notice comparison' {
    param($f)
    Write-Utf8Raw (Join-Path $f.Root 'third_party/example/PATENTS') ('valid' + [string][char]0x85 + 'payload')
    Sync-AssetFileMetadata $f 'third_party/example/PATENTS'
    Write-Json $f.AssetPath $f.AssetManifest
} @('LICENSE_NOTICE_RENDER_INVALID')

Invoke-Case 'unknown dependency SPDX' {
    param($f)
    $f.DependencyManifest.modules[0].declared_license_expression = 'LicenseRef-Unknown'
    Write-ManifestsAndNotice $f
} @('LICENSE_UNKNOWN_SPDX')

Invoke-Case 'missing distributed asset entry' {
    param($f)
    $f.AssetManifest.assets = @($f.AssetManifest.assets[0])
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_SET_MISMATCH')

Invoke-Case 'extra distributed asset entry' {
    param($f)
    $extra = [ordered]@{
        path = 'schemas/missing.json'
        size = 0
        sha256 = '0' * 64
        origin = 'project-owned'
        source_url = 'project://github.com/endview/freeagent'
        spdx_expression = 'AGPL-3.0-only'
        notice_id = ''
        required_files = @()
    }
    $f.AssetManifest.assets = @($f.AssetManifest.assets[0], $extra, $f.AssetManifest.assets[1])
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_SET_MISMATCH')

Invoke-Case 'duplicate distributed asset entry' {
    param($f)
    $f.AssetManifest.assets = @($f.AssetManifest.assets[0], $f.AssetManifest.assets[0], $f.AssetManifest.assets[1])
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_DUPLICATE')

Invoke-Case 'distributed asset hash mismatch' {
    param($f)
    $entry = Get-AssetEntry $f 'examples/project.json'
    $entry.sha256 = '0' * 64
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_HASH_MISMATCH')

Invoke-Case 'third-party asset license body is required' {
    param($f)
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $entry.required_files = @()
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_REQUIRED')

Invoke-Case 'third-party asset legal bundle cannot omit same-directory NOTICE' {
    param($f)
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $entry.required_files = @($entry.required_files | Where-Object { [string]$_.path -cne 'third_party/example/NOTICE' })
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_SET_MISMATCH')

Invoke-Case 'third-party asset legal bundle cannot omit nested LICENSES file' {
    param($f)
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $entry.required_files = @($entry.required_files | Where-Object { [string]$_.path -cne 'third_party/example/LICENSES/MIT.txt' })
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_SET_MISMATCH')

Invoke-Case 'third-party asset cannot disguise arbitrary text as a license' {
    param($f)
    $tool = Get-Item -LiteralPath (Join-Path $f.Root 'third_party/example/tool.go')
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $entry.required_files += New-RequiredFileRecord 'third_party/example/tool.go' 'license' $tool
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_SET_MISMATCH')

Invoke-Case 'third-party asset legal role is derived from basename' {
    param($f)
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $noticeRecord = @($entry.required_files | Where-Object { [string]$_.path -ceq 'third_party/example/NOTICE' })[0]
    $noticeRecord.role = 'license'
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_ROLE_MISMATCH')

Invoke-Case 'blank asset legal body is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'third_party/example/PATENTS') ''
    Sync-AssetFileMetadata $f 'third_party/example/PATENTS'
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_LEGAL_TEXT_INVALID')

Invoke-Case 'asset required path rejects Markdown table injection' {
    param($f)
    $entry = Get-AssetEntry $f 'testdata/external.txt'
    $licenseRecord = @($entry.required_files | Where-Object { [string]$_.path -ceq 'third_party/example/LICENSE' })[0]
    $licenseRecord.path = 'third_party/example/LICENSE|NOTICE'
    Write-Json $f.AssetPath $f.AssetManifest
} @('LICENSE_ASSET_SCHEMA_INVALID')

Invoke-Case 'project-owned Go SPDX remains AGPL only' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '// SPDX-License-Identifier: MIT'
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go SPDX expression is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '// SPDX-License-Identifier: AGPL-3.0-only OR MIT'
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go block-comment SPDX marker is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '/* SPDX-License-Identifier: AGPL-3.0-only */'
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go SPDX trailing text is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '// SPDX-License-Identifier: AGPL-3.0-only trailing'
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go multiline SPDX marker is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') "// SPDX-License-Identifier:`nAGPL-3.0-only"
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go split SPDX marker is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') "// SPDX-`nLicense-Identifier: AGPL-3.0-only"
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go identifier word split is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') "// SPDX-License-Identi`n// fier: AGPL-3.0-only"
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go SPDX word split is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') "// SP`n// DX-License-Identifier: AGPL-3.0-only"
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'project Go bare SPDX line is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '// SPDX compatibility note'
} @('LICENSE_GO_SPDX_CONFLICT')

Invoke-Case 'third-party Go SPDX must match asset provenance' {
    param($f)
    $goPath = Join-Path $f.Root 'third_party/example/tool.go'
    Write-Utf8Canonical $goPath "// SPDX-License-Identifier: Apache-2.0`npackage example"
    Sync-AssetFileMetadata $f 'third_party/example/tool.go'
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_GO_SPDX_CONFLICT')

Invoke-Case 'third-party Go malformed SPDX marker is rejected' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'third_party/example/tool.go') '/* SPDX-License-Identifier: MIT */'
    Sync-AssetFileMetadata $f 'third_party/example/tool.go'
    Write-ManifestsAndNotice $f
} @('LICENSE_ASSET_GO_SPDX_CONFLICT')

Invoke-Case 'deterministic notice mismatch' {
    param($f)
    Write-Utf8Canonical $f.NoticesPath '# stale notice'
} @('LICENSE_NOTICE_MISMATCH')

Invoke-Case 'embedded dependency license text tamper' {
    param($f)
    $text = [IO.File]::ReadAllText($f.NoticesPath, [Text.Encoding]::UTF8)
    Write-Utf8Canonical $f.NoticesPath $text.Replace('Example dependency license', 'Tampered dependency license')
} @('LICENSE_NOTICE_MISMATCH')

Invoke-Case 'embedded asset license text tamper' {
    param($f)
    $text = [IO.File]::ReadAllText($f.NoticesPath, [Text.Encoding]::UTF8)
    Write-Utf8Canonical $f.NoticesPath $text.Replace('Example asset license', 'Tampered asset license')
} @('LICENSE_NOTICE_MISMATCH')

Invoke-Case 'nested repository reparse path rejected without traversal' {
    param($f)
    $target = Join-Path (Split-Path -Parent $f.Root) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N') + ' reparse target')
    New-Item -ItemType Directory -Path $target | Out-Null
    $f.CleanupRoots += $target
    $f.SensitivePaths += $target
    Write-Utf8Canonical (Join-Path $target 'poison.go') '// SPDX-License-Identifier: MIT'
    $link = Join-Path $f.Root 'examples/reparse'
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        New-Item -ItemType Junction -Path $link -Target $target | Out-Null
    } else {
        New-Item -ItemType SymbolicLink -Path $link -Target $target | Out-Null
    }
} @('LICENSE_GO_SCAN_REPARSE_FORBIDDEN')

Invoke-Case 'nested module archive reparse path is rejected without traversal' {
    param($f)
    $external = Join-Path (Split-Path -Parent $f.Root) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N') + ' external archive')
    New-Item -ItemType Directory -Path $external | Out-Null
    foreach ($item in @(Get-ChildItem -LiteralPath $f.DependencyDirectory -Force)) {
        Copy-Item -LiteralPath $item.FullName -Destination $external -Recurse -Force
    }
    $f.CleanupRoots += $external
    $f.SensitivePaths += $external
    $target = Join-Path (Split-Path -Parent $f.Root) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N') + ' archive target')
    New-Item -ItemType Directory -Path $target | Out-Null
    Write-Utf8Canonical (Join-Path $target 'hidden.txt') '// SPDX-License-Identifier: LicenseRef-Should-Not-Be-Traversed'
    $f.CleanupRoots += $target
    $f.SensitivePaths += $target
    $link = Join-Path $external 'nested-link'
    if ($testIsWindows) {
        New-Item -ItemType Junction -Path $link -Target $target | Out-Null
    } else {
        New-Item -ItemType SymbolicLink -Path $link -Target $target | Out-Null
    }
    $f.DependencyDirectory = $external
    $f.GoCommand = New-FakeGo $f.Root $external
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_ARCHIVE_REPARSE_FORBIDDEN')

Invoke-Case 'module directory ancestor reparse is rejected before enumeration' {
    param($f)
    $alias = Join-Path (Split-Path -Parent $f.Root) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N') + ' module alias')
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        New-Item -ItemType Junction -Path $alias -Target $f.DependencyDirectory | Out-Null
    } else {
        New-Item -ItemType SymbolicLink -Path $alias -Target $f.DependencyDirectory | Out-Null
    }
    $f.CleanupLinks += $alias
    $f.SensitivePaths += $alias
    $f.GoCommand = New-FakeGo $f.Root $alias
} @('LICENSE_MODULE_REPARSE_FORBIDDEN')

Invoke-Case 'fake Go command failure' {
    param($f)
    $absoluteLeak = if ($testIsWindows) {
        'Z' + [char]0x3a + [char]0x5c + 'outside secret' + [char]0x5c + 'module'
    } else {
        [string][char]0x2f + 'opt/private/module'
    }
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at " + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go module-root mismatch redacts an arbitrary absolute path' {
    param($f)
    $absoluteLeak = if ($testIsWindows) {
        'Y' + [char]0x3a + [char]0x5c + 'outside module' + [char]0x5c + 'go.mod'
    } else {
        [string][char]0x2f + 'srv/private/go.mod'
    }
    $leakLiteral = $absoluteLeak.Replace("'", "''")
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output '__ABSOLUTE_LEAK__'
    return
}
throw 'unexpected fake Go arguments'
'@
    [IO.File]::WriteAllText($f.GoCommand, $content.Replace('__ABSOLUTE_LEAK__', $leakLiteral), [Text.UTF8Encoding]::new($true))
    $f.SensitivePaths += $absoluteLeak
} @('LICENSE_GO_MODULE_ROOT_MISMATCH')

Invoke-Case 'Go failure redacts an arbitrary UNC absolute path' {
    param($f)
    $separator = [string][char]0x5c
    $absoluteLeak = $separator + $separator + 'server' + $separator + 'private' + $separator + 'module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at " + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts an arbitrary forward-slash network path' {
    param($f)
    $absoluteLeak = ([string][char]0x2f) + [char]0x2f + 'server/private/module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at " + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a colon-prefixed POSIX absolute path' {
    param($f)
    $absoluteLeak = ([string][char]0x2f) + 'home/private/module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at path:" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a colon-prefixed Windows absolute path' {
    param($f)
    $separator = [string][char]0x5c
    $absoluteLeak = 'X' + [char]0x3a + $separator + 'private' + $separator + 'module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at path:" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts an absolute file URI' {
    param($f)
    $fileURI = 'fi' + 'le:' + ([string][char]0x2f) + [char]0x2f + 'server/private/module'
    $f.SensitivePaths += $fileURI
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at " + $fileURI + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a letter-prefixed Windows absolute path' {
    param($f)
    $separator = [string][char]0x5c
    $absoluteLeak = 'X' + [char]0x3a + $separator + 'private' + $separator + 'module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at prefix" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a letter-prefixed POSIX absolute path' {
    param($f)
    $absoluteLeak = ([string][char]0x2f) + 'opt/private/module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at prefix" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a letter-prefixed UNC absolute path' {
    param($f)
    $separator = [string][char]0x5c
    $absoluteLeak = $separator + $separator + 'server' + $separator + 'private' + $separator + 'module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at prefix" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a letter-prefixed network absolute path' {
    param($f)
    $absoluteLeak = ([string][char]0x2f) + [char]0x2f + 'server/private/module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at prefix" + $absoluteLeak + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-Case 'Go failure redacts a letter-prefixed file URI' {
    param($f)
    $fileURI = 'fi' + 'le:' + ([string][char]0x2f) + [char]0x2f + 'server/private/module'
    $f.SensitivePaths += $fileURI
    Write-Utf8Canonical $f.GoCommand ("throw 'fixture Go failed at prefix" + $fileURI + "'")
} @('LICENSE_GO_COMMAND_FAILED')

Invoke-DiagnosticCase 'untrusted Go diagnostic is omitted before path redaction' {
    param($f)
    $separator = [string][char]0x5c
    $absoluteLeak = 'X' + [char]0x3a + $separator + 'private' + $separator + 'module'
    $f.SensitivePaths += $absoluteLeak
    Write-Utf8Canonical $f.GoCommand ("throw 'untrusted internal/project.go example.org/dep prefix" + $absoluteLeak + "'")
} 'LICENSE_GO_COMMAND_FAILED' @('subprocess diagnostics omitted')

Invoke-DiagnosticCase 'untrusted selected-module payload is omitted before diagnostics' {
    param($f)
    $absoluteLeak = ([string][char]0x2f) + 'private/secret'
    $f.SensitivePaths += $absoluteLeak
    $rootLiteral = $f.Root.Replace("'", "''")
    $content = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
$fixtureRoot = '__ROOT__'
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'env' -and $GoArgs[1] -ceq 'GOMOD') {
    Write-Output (Join-Path $fixtureRoot 'go.mod')
    return
}
if ($GoArgs.Count -eq 2 -and $GoArgs[0] -ceq 'mod' -and $GoArgs[1] -ceq 'verify') { return }
if ($GoArgs.Count -ge 1 -and $GoArgs[0] -ceq 'list') {
    [pscustomobject]@{ Path = 'github.com/endview/freeagent'; Main = $true } | ConvertTo-Json -Compress | Write-Output
    [pscustomobject]@{ Path = 'prefix/private/secret'; Replace = [pscustomobject]@{ Path = 'replacement' } } | ConvertTo-Json -Compress | Write-Output
    return
}
throw 'unexpected fake Go arguments'
'@
    [IO.File]::WriteAllText($f.GoCommand, $content.Replace('__ROOT__', $rootLiteral), [Text.UTF8Encoding]::new($true))
} 'LICENSE_GO_REPLACE_FORBIDDEN' @('selected-module payload omitted')

Invoke-DiagnosticCase 'repository-relative Go path remains actionable' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.Root 'internal/project.go') '// SPDX-License-Identifier: MIT'
} 'LICENSE_GO_SPDX_CONFLICT' @('internal/project.go')

Invoke-DiagnosticCase 'module identity remains actionable' {
    param($f)
    Write-Utf8Canonical (Join-Path $f.DependencyDirectory 'LICENSES/Apache-2.0.txt') ([string][char]0x200b)
    Sync-DependencyFileMetadata $f 'LICENSES/Apache-2.0.txt'
    Write-ManifestsAndNotice $f
} 'LICENSE_DEPENDENCY_TEXT_INVALID' @('example.org/dep', 'LICENSES/Apache-2.0.txt')

$colonBearingLogicalModule = 'https://example.invalid/docs/a' + [char]0x3a + '/project.go'
Invoke-DiagnosticCase 'URL-like and colon-bearing relative module identity is preserved' {
    param($f)
    $logicalModule = $colonBearingLogicalModule
    $f.DependencyManifest.modules[0].path = $logicalModule
    $f.DependencyManifest.modules[0].notice_id = Get-NoticeID 'go' ($logicalModule + '@v1.2.3')
    $f.DependencyManifest.modules[0].declared_license_expression = 'LicenseRef-Unknown'
    $fakeGo = [IO.File]::ReadAllText($f.GoCommand, [Text.Encoding]::UTF8).Replace('example.org/dep', $logicalModule)
    [IO.File]::WriteAllText($f.GoCommand, $fakeGo, [Text.UTF8Encoding]::new($true))
    Write-ManifestsAndNotice $f
} 'LICENSE_UNKNOWN_SPDX' @($colonBearingLogicalModule)

Invoke-Case 'external module-cache error uses logical path only' {
    param($f)
    $external = Join-Path (Split-Path -Parent $f.Root) ($fixtureNamePrefix + [guid]::NewGuid().ToString('N') + ' external module')
    New-Item -ItemType Directory -Path $external | Out-Null
    $f.CleanupRoots += $external
    $f.SensitivePaths += $external
    Write-Utf8Canonical (Join-Path $external 'go.mod') "module example.org/dep`n`ngo 1.20"
    [IO.File]::WriteAllBytes((Join-Path $external 'LICENSE'), [byte[]](0xff, 0xfe, 0x41))
    Write-Utf8Canonical (Join-Path $external 'LICENSES/Apache-2.0.txt') 'Example nested dependency license'
    Write-Utf8Canonical (Join-Path $external 'NOTICE') 'Example dependency notice'
    Write-Utf8Canonical (Join-Path $external 'PATENTS') 'Example dependency patent grant'
    $f.DependencyDirectory = $external
    foreach ($relative in @('LICENSE', 'LICENSES/Apache-2.0.txt', 'NOTICE', 'PATENTS')) {
        Sync-DependencyFileMetadata $f $relative $external
    }
    $f.GoCommand = New-FakeGo $f.Root $external
    Write-ManifestsAndNotice $f
} @('LICENSE_DEPENDENCY_TEXT_INVALID')

Invoke-Case 'subprocess uses the current PowerShell engine' {
    param($f)
    $result = Invoke-CheckerProcess $f.Root $f.GoCommand 15000 $f.CheckerPath
    Assert-CleanCheckerOutput $result $f.Root $f.GoCommand
    $expectedEngine = Get-CurrentPowerShellEnginePath
    if (-not $result.EnginePath.Equals($expectedEngine, $testPathComparison)) { throw 'checker subprocess used a different PowerShell engine' }
    if ($result.ExitCode -ne 0 -or $result.TimedOut) { throw 'current-engine checker smoke failed' }
} @()

Invoke-Case 'subprocess drains large stdout and stderr concurrently' {
    param($f)
    $original = [IO.File]::ReadAllBytes($f.GoCommand)
    try {
        $noisy = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GoArgs)
[Console]::Out.Write(('O' * 262144))
[Console]::Error.Write(('E' * 262144))
Write-Output 'not-a-path'
'@
        [IO.File]::WriteAllText($f.GoCommand, $noisy, [Text.UTF8Encoding]::new($true))
        $result = Invoke-CheckerProcess $f.Root $f.GoCommand 10000 $f.CheckerPath
        Assert-CleanCheckerOutput $result $f.Root $f.GoCommand
        if ($result.TimedOut -or $result.ExitCode -eq 0 -or $result.Stdout.Length -lt 262144 -or $result.Stderr.Length -lt 262144) {
            throw 'large-output checker process was not drained concurrently'
        }
    } finally {
        [IO.File]::WriteAllBytes($f.GoCommand, $original)
    }
} @()

Invoke-Case 'subprocess timeout terminates checker safely' {
    param($f)
    $original = [IO.File]::ReadAllBytes($f.GoCommand)
    try {
        [IO.File]::WriteAllText($f.GoCommand, "Start-Sleep -Seconds 30`n", [Text.UTF8Encoding]::new($true))
        $result = Invoke-CheckerProcess $f.Root $f.GoCommand 500 $f.CheckerPath
        Assert-CleanCheckerOutput $result $f.Root $f.GoCommand
        if (-not $result.TimedOut) { throw 'checker timeout fixture did not time out' }
        if ($null -ne (Get-Process -Id $result.ProcessId -ErrorAction SilentlyContinue)) { throw 'timed-out checker process is still running' }
    } finally {
        [IO.File]::WriteAllBytes($f.GoCommand, $original)
    }
} @()

if (-not $testIsWindows) {
    $archiveFifoFixture = New-ValidFixture
    try {
        $archiveFifoPath = Join-Path $archiveFifoFixture.DependencyDirectory 'opaque.fifo'
        New-UnixFifo $archiveFifoPath
        $archiveFifoResult = Invoke-CheckerProcess $archiveFifoFixture.Root $archiveFifoFixture.GoCommand 5000 $archiveFifoFixture.CheckerPath
        Assert-CleanCheckerOutput $archiveFifoResult $archiveFifoFixture.Root $archiveFifoFixture.GoCommand $archiveFifoFixture.SensitivePaths
        if ($archiveFifoResult.TimedOut) { throw 'module archive FIFO reached a content read and blocked the checker' }
        if ($archiveFifoResult.ExitCode -eq 0 -or $archiveFifoResult.Stderr.IndexOf('[LICENSE_DEPENDENCY_ARCHIVE_FILE_INVALID]', [StringComparison]::Ordinal) -lt 0) {
            throw "module archive FIFO was not rejected before byte reads: $($archiveFifoResult.Stderr)"
        }
        $script:passed++
        Write-Output 'PASS fixture: Linux module archive FIFO is classified before byte reads'
    } finally {
        Remove-Fixture $archiveFifoFixture.Root
    }

    $fifoFixture = New-ValidFixture
    try {
        $fifoPath = Join-Path $fifoFixture.Root 'LICENSE'
        Remove-Item -LiteralPath $fifoPath -Force
        New-UnixFifo $fifoPath
        $fifoResult = Invoke-CheckerProcess $fifoFixture.Root $fifoFixture.GoCommand 5000 $fifoFixture.CheckerPath
        Assert-CleanCheckerOutput $fifoResult $fifoFixture.Root $fifoFixture.GoCommand $fifoFixture.SensitivePaths
        if ($fifoResult.TimedOut) { throw 'no-writer FIFO reached a content read and blocked the checker' }
        if ($fifoResult.ExitCode -eq 0 -or $fifoResult.Stderr.IndexOf('[LICENSE_ROOT_MISSING]', [StringComparison]::Ordinal) -lt 0) {
            throw "no-writer FIFO was not rejected by the regular-file probe: $($fifoResult.Stderr)"
        }
        if ($null -ne (Get-Process -Id $fifoResult.ProcessId -ErrorAction SilentlyContinue)) { throw 'FIFO checker child process is still running' }
        $script:passed++
        Write-Output 'PASS fixture: Linux no-writer FIFO is classified before byte reads'
    } finally {
        Remove-Fixture $fifoFixture.Root
    }
}

Invoke-Case 'relative root rejected' {
    param($f)
    $result = Invoke-CheckerProcess 'relative-root' $f.GoCommand 15000 $f.CheckerPath
    Assert-CleanCheckerOutput $result $f.Root $f.GoCommand
    if ($result.ExitCode -eq 0 -or $result.Stderr.IndexOf('[LICENSE_ROOT_INVALID]', [StringComparison]::Ordinal) -lt 0) {
        throw "relative root failure was not clean: $($result.Stderr)"
    }
} @()

Invoke-Case 'absolute root is independent of CWD and Windows drive-letter case' {
    param($f)
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        $f.Root = $f.Root.Substring(0, 1).ToLowerInvariant() + $f.Root.Substring(1)
    }
} @()

Invoke-Case 'filesystem root canonicalization preserves the root separator' {
    param($f)
    $filesystemRoot = [IO.Path]::GetPathRoot($projectRoot)
    $result = Invoke-CheckerProcess $filesystemRoot $null 15000 $f.CheckerPath
    Assert-CleanCheckerOutput $result $filesystemRoot $null
    if ($result.TimedOut -or $result.ExitCode -eq 0 -or
        $result.Stderr.IndexOf('[LICENSE_ROOT_MISSING]', [StringComparison]::Ordinal) -lt 0 -or
        $result.Stderr.IndexOf('required file is missing: LICENSE', [StringComparison]::Ordinal) -lt 0 -or
        $result.Stderr.IndexOf('<root>', [StringComparison]::Ordinal) -ge 0) {
        throw "filesystem root was not preserved canonically: $($result.Stderr)"
    }
} @()

Invoke-Case 'lexical root parent reparse chain rejected' {
    param($f)
    $parent = Split-Path -Parent $f.Root
    $aliasParent = Join-Path $parent ('freeagent parent link ' + [guid]::NewGuid().ToString('N'))
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        New-Item -ItemType Junction -Path $aliasParent -Target $parent | Out-Null
    } else {
        New-Item -ItemType SymbolicLink -Path $aliasParent -Target $parent | Out-Null
    }
    try {
        $aliasRoot = Join-Path $aliasParent (Split-Path -Leaf $f.Root)
        $result = Invoke-CheckerProcess $aliasRoot $f.GoCommand 15000 $f.CheckerPath
        Assert-CleanCheckerOutput $result $aliasRoot $f.GoCommand
        if ($result.ExitCode -eq 0 -or $result.Stderr.IndexOf('[LICENSE_ROOT_REPARSE_FORBIDDEN]', [StringComparison]::Ordinal) -lt 0) {
            throw "lexical reparse parent was not rejected cleanly: $($result.Stderr)"
        }
    } finally {
        if (Test-Path -LiteralPath $aliasParent) {
            $aliasItem = Get-Item -Force -LiteralPath $aliasParent
            if (($aliasItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { throw 'refusing to remove a non-reparse alias parent' }
            [IO.Directory]::Delete($aliasParent, $false)
        }
    }
} @()

Write-Output "PASS Test-License self-tests: $passed fixtures"
