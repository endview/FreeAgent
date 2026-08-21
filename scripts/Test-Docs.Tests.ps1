[CmdletBinding()]
param([string]$GofmtPath = '')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:GatePath = Join-Path $PSScriptRoot 'Test-Docs.ps1'
$script:RepositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:PowerShellPath = (Get-Process -Id $PID).Path
$script:Failures = New-Object 'System.Collections.Generic.List[string]'
$script:CaseCount = 0
$script:IsWindowsPlatform = [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
$script:GofmtPath = $GofmtPath
if ([string]::IsNullOrWhiteSpace($script:GofmtPath)) {
    $gofmtCommand = Get-Command -Name 'gofmt' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $gofmtCommand) { throw 'SELFTEST_GOFMT_MISSING' }
    $script:GofmtPath = if ($gofmtCommand.PSObject.Properties.Name -contains 'Source') { [string]$gofmtCommand.Source } else { [string]$gofmtCommand.Path }
}
if (-not [System.IO.Path]::IsPathRooted($script:GofmtPath) -or -not (Test-Path -LiteralPath $script:GofmtPath -PathType Leaf)) {
    throw 'SELFTEST_GOFMT_INVALID'
}
$script:GofmtPath = [System.IO.Path]::GetFullPath((Get-Item -LiteralPath $script:GofmtPath -Force).FullName)
$script:SpecRelocationManifestRelative = 'docs/architecture/specs-relocation.v1.json'
$script:CurrentSpecEntries = @(
    [pscustomobject]@{ Path = 'docs/specs/CORE_RUNTIME_V1.md'; Status = 'S1_ACCEPTED_DEVELOPMENT_BASELINE'; IndexLink = '../../specs/CORE_RUNTIME_V1.md' },
    [pscustomobject]@{ Path = 'docs/specs/CURRENT_STORE_V1.md'; Status = 'S1_SCHEMA_DRAFT_DEVELOPMENT_BASELINE_ACCEPTED'; IndexLink = '../../specs/CURRENT_STORE_V1.md' },
    [pscustomobject]@{ Path = 'docs/CUTOVER_ACCEPTANCE.md'; Status = 'CUTOVER_NOT_APPLICABLE_NEVER_DEPLOYED'; IndexLink = '../../CUTOVER_ACCEPTANCE.md' }
)
$script:HistoricalSpecEntries = @(
    [pscustomobject]@{ Name = '2026-07-18-compatibility-remediation-b-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-19-task3-lossless-persistence-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-20-locked-mcp-evidence-first-discovery-design.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' },
    [pscustomobject]@{ Name = '2026-07-20-storage-backend-b-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-21-runtime-catalog-authority-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-22-governance-rule-wire-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-22-governance-rule-wire-v1-lock.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-22-p0-spec-lock-attestation.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-26-stage2-unified-assembly-cutover.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
    [pscustomobject]@{ Name = '2026-07-28-stage3-sealed-content-parents.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' },
    [pscustomobject]@{ Name = '2026-07-28-stage3-skill-lifecycle-lock.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' }
)

function Get-Sha256Hex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = $sha.ComputeHash($Bytes)
    } finally {
        $sha.Dispose()
    }
    return ([System.BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
}

function Get-LineSha256 {
    param([AllowEmptyString()][string]$Line)
    return Get-Sha256Hex -Bytes $script:Utf8NoBom.GetBytes($Line)
}

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [AllowEmptyString()][string]$Text
    )
    $directory = [System.IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrEmpty($directory)) {
        [void](New-Item -ItemType Directory -Path $directory -Force)
    }
    [System.IO.File]::WriteAllBytes($Path, $script:Utf8NoBom.GetBytes($Text))
}

function Join-FixturePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )
    return Join-Path $Root ($RelativePath.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
}

function New-DirectoryLink {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Target
    )
    $itemType = if ($script:IsWindowsPlatform) { 'Junction' } else { 'SymbolicLink' }
    [void](New-Item -ItemType $itemType -Path $Path -Target $Target -Force)
}

function Remove-DirectoryLink {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (Test-Path -LiteralPath $Path) {
        if ($script:IsWindowsPlatform) {
            [System.IO.Directory]::Delete($Path)
        } else {
            [System.IO.File]::Delete($Path)
        }
    }
}

function Copy-RawFile {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    [void](New-Item -ItemType Directory -Path ([System.IO.Path]::GetDirectoryName($Destination)) -Force)
    [System.IO.File]::WriteAllBytes($Destination, [System.IO.File]::ReadAllBytes($Source))
}

function Update-ClaimsManifest {
    param([Parameter(Mandatory = $true)][string]$Root)

    $readmePath = Join-Path $Root 'README.md'
    $maturityPath = Join-FixturePath -Root $Root -RelativePath 'docs/RELEASE_MATURITY.md'
    $capabilityByTitle = @{
        'Planned Capability' = 'fixture.cap-planned'
        'Unverified Capability' = 'fixture.cap-unverified'
        'Stable_MODE Capability' = 'fixture.cap-stable'
    }
    $byKey = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    $keys = New-Object 'System.Collections.Generic.List[string]'

    foreach ($source in @(
        [pscustomobject]@{ Path = 'README.md'; FullPath = $readmePath },
        [pscustomobject]@{ Path = 'docs/RELEASE_MATURITY.md'; FullPath = $maturityPath }
    )) {
        $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($source.FullPath))
        $lines = [System.Text.RegularExpressions.Regex]::Split($text, "`r`n|`n|`r")
        foreach ($line in $lines) {
            $id = $null
            $status = $null
            $policy = $null
            $kind = 'CAPABILITY'
            $match = [System.Text.RegularExpressions.Regex]::Match($line, '^(?<id>fixture\.cap-[a-z]+) is (?<status>stable|experimental|unverified|planned|excluded)\.$')
            if ($match.Success) {
                $id = $match.Groups['id'].Value
                $status = $match.Groups['status'].Value
            } else {
                $match = [System.Text.RegularExpressions.Regex]::Match($line, '^#{1,6}\s+(?<title>.+?)\s+\[(?<status>stable|experimental|unverified|planned|excluded)\]\s*$')
                if ($match.Success -and $capabilityByTitle.ContainsKey($match.Groups['title'].Value)) {
                    $id = $capabilityByTitle[$match.Groups['title'].Value]
                    $status = $match.Groups['status'].Value
                } elseif ($line -ceq 'Stable means a capability admitted by named executable evidence.') {
                    $kind = 'POLICY'
                    $policy = [ordered]@{ syntax = 'DEFINITION'; subject = 'MATURITY' }
                } else {
                    continue
                }
            }

            $hash = Get-LineSha256 -Line $line
            $key = $source.Path + [char]0 + $hash
            $claims = @()
            if ($kind -ceq 'CAPABILITY') {
                $claims = @(
                    [ordered]@{
                        capability_id = $id
                        claimed_status = $status
                    }
                )
            }
            $entry = [ordered]@{
                path = $source.Path
                line_sha256 = $hash
                kind = $kind
                claims = $claims
                policy = $policy
                reason = 'fixture maturity statement'
            }
            $byKey.Add($key, $entry)
            $keys.Add($key)
        }
    }
    $keyArray = $keys.ToArray()
    [System.Array]::Sort($keyArray, [System.StringComparer]::Ordinal)
    $entries = @($keyArray | ForEach-Object { $byKey[$_] })
    $manifest = [ordered]@{
        schema_version = 'v1'
        entries = $entries
    }
    $json = $manifest | ConvertTo-Json -Depth 10
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath 'testdata/release/docs-maturity-claims.v1.json') -Text ($json + "`n")
}

function New-BaseFixture {
    param([Parameter(Mandatory = $true)][string]$Root)

    [void](New-Item -ItemType Directory -Path $Root -Force)
    foreach ($entry in $script:HistoricalSpecEntries) {
        $relative = 'docs/architecture/specs/' + $entry.Name
        $text = @(
            ('# Historical fixture: ' + $entry.Name)
            ''
            ('> Machine status: `' + $entry.Status + '`')
            ''
        ) -join "`n"
        Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath $relative) -Text $text
    }

    foreach ($entry in $script:CurrentSpecEntries) {
        $title = [System.IO.Path]::GetFileNameWithoutExtension([string]$entry.Path)
        $machineStatus = [string]$entry.Status
        $text = "# $title`n`nMachine status: ``$machineStatus```n"
        Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath $entry.Path) -Text $text
    }

    $indexLines = New-Object 'System.Collections.Generic.List[string]'
    $indexLines.Add('# Historical architecture specification index')
    $indexLines.Add('')
    $indexLines.Add('> Machine status: `HISTORICAL_INDEX_NON_NORMATIVE`')
    $indexLines.Add('')
    foreach ($entry in $script:CurrentSpecEntries) {
        $indexLines.Add(('- [' + ([System.IO.Path]::GetFileNameWithoutExtension($entry.Path)) + '](' + $entry.IndexLink + ')'))
    }
    $indexLines.Add('')
    foreach ($entry in $script:HistoricalSpecEntries) {
        $indexLines.Add(('| `' + $entry.Name + '` | `' + $entry.Status + '` |'))
    }
    $indexLines.Add('')
    Write-Utf8NoBom `
        -Path (Join-FixturePath -Root $Root -RelativePath 'docs/architecture/specs/README.md') `
        -Text (($indexLines.ToArray()) -join "`n")

    $authorityManifest = [ordered]@{
        schema_version = 2
        kind = 'freeagent-architecture-spec-authority-index'
        index = [ordered]@{
            path = 'docs/architecture/specs/README.md'
            status = 'HISTORICAL_INDEX_NON_NORMATIVE'
        }
        current_authorities = @($script:CurrentSpecEntries | ForEach-Object {
            [ordered]@{ path = $_.Path; status = $_.Status }
        })
        historical_sources = @($script:HistoricalSpecEntries | ForEach-Object {
            [ordered]@{
                path = 'docs/architecture/specs/' + $_.Name
                status = $_.Status
            }
        })
    }
    $authorityJson = $authorityManifest | ConvertTo-Json -Depth 8
    $authorityJson = $authorityJson.Replace("`r`n", "`n").Replace("`r", "`n")
    Write-Utf8NoBom `
        -Path (Join-FixturePath -Root $Root -RelativePath $script:SpecRelocationManifestRelative) `
        -Text ($authorityJson + "`n")

    $seedRelative = 'internal/currentstore/schema.go'
    $seedSource = Join-Path $script:RepositoryRoot ($seedRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    if (-not (Test-Path -LiteralPath $seedSource -PathType Leaf)) {
        throw 'SELFTEST_SEED_SOURCE_MISSING'
    }
    Copy-RawFile -Source $seedSource -Destination (Join-Path $Root ($seedRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar)))

    $nonAscii = ([string][char]0x6307) + ([string][char]0x5357)
    $guideName = 'guide file-' + $nonAscii + '.md'
    $readme = @(
        '# Fixture'
        ''
        ('[guide](<docs/' + $guideName + '#intro>)')
        '![asset](<assets/pixel data.bin>)'
        '[external](https://github.com/example/reference)'
        ('<a href="docs/' + $guideName + '#intro">HTML guide</a>')
        '<img src="assets/pixel data.bin">'
        '<source src=''assets/pixel data.bin''>'
        '<a id="map"></a>'
        '<area href="assets/pixel.bin" ping="assets/pixel.bin assets/pixel-alt.bin">'
        '<img src="assets/pixel.bin" srcset="assets/pixel.bin 1x, assets/pixel-alt.bin 2x" longdesc="assets/pixel-alt.bin" usemap="#map">'
        '<source src="assets/pixel.bin" srcset="assets/pixel.bin 320w, assets/pixel-alt.bin 640w">'
        '<video src="assets/pixel.bin" poster="assets/pixel-alt.bin"></video>'
        '<audio src="assets/pixel.bin"></audio>'
        '<track src="assets/pixel-alt.bin">'
        '<form action="assets/pixel.bin"></form>'
        '<input src="assets/pixel.bin" formaction="assets/pixel-alt.bin">'
        '<button formaction="assets/pixel.bin">submit</button>'
        '<blockquote cite="assets/pixel.bin">quote</blockquote>'
        '<q cite="assets/pixel.bin">quote</q>'
        '<del cite="assets/pixel.bin">old</del>'
        '<ins cite="assets/pixel-alt.bin">new</ins>'
        '<html manifest="assets/pixel.bin">'
        '<body background="assets/pixel.bin">'
        '<table background="assets/pixel.bin"><td background="assets/pixel.bin"></td><th background="assets/pixel-alt.bin"></th></table>'
        '<use href="assets/pixel.bin" xlink:href="assets/pixel-alt.bin">'
        '<image href="assets/pixel.bin" xlink:href="assets/pixel-alt.bin">'
        ''
        'fixture.cap-planned is planned.'
        'fixture.cap-unverified is unverified.'
        'fixture.cap-stable is stable.'
        'Stable means a capability admitted by named executable evidence.'
        ''
    ) -join "`n"
    Write-Utf8NoBom -Path (Join-Path $Root 'README.md') -Text $readme
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath ('docs/' + $guideName)) -Text "<a id=`"intro`"></a>`n`n# Guide`n"
    [void](New-Item -ItemType Directory -Path (Join-Path $Root 'assets') -Force)
    [System.IO.File]::WriteAllBytes((Join-FixturePath -Root $Root -RelativePath 'assets/pixel data.bin'), [byte[]]@(0, 1, 2, 3))
    [System.IO.File]::WriteAllBytes((Join-FixturePath -Root $Root -RelativePath 'assets/pixel.bin'), [byte[]]@(4, 5, 6, 7))
    [System.IO.File]::WriteAllBytes((Join-FixturePath -Root $Root -RelativePath 'assets/pixel-alt.bin'), [byte[]]@(8, 9, 10, 11))

    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath 'internal/core/evidence_test.go') -Text @'
package core

import "testing"

func TestFixtureStableEvidence(t *testing.T) {}
'@

    $matrix = [ordered]@{
        schema_version = 'v1'
        items = @(
            [ordered]@{ id = 'fixture.cap-planned'; title = 'Planned Capability'; status = 'planned'; owner = 'core'; tests = @(); docs_anchor = 'cap-planned'; external_evidence = @() },
            [ordered]@{ id = 'fixture.cap-unverified'; title = 'Unverified Capability'; status = 'unverified'; owner = 'core'; tests = @(); docs_anchor = 'cap-unverified'; external_evidence = @() },
            [ordered]@{
                id = 'fixture.cap-stable'
                title = 'Stable_MODE Capability'
                status = 'stable'
                owner = 'core'
                tests = @([ordered]@{ kind = 'go'; package = './internal/core'; name = 'TestFixtureStableEvidence' })
                docs_anchor = 'cap-stable'
                external_evidence = @()
            }
        )
    }
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath 'testdata/release/capabilities.v1.json') -Text (($matrix | ConvertTo-Json -Depth 10) + "`n")

    $maturity = @(
        '# Release Maturity'
        ''
        '<a id="cap-planned"></a>'
        '## Planned Capability [planned]'
        ''
        '<a id="cap-unverified"></a>'
        '## Unverified Capability [unverified]'
        ''
        '<a id="cap-stable"></a>'
        '## Stable_MODE Capability [stable]'
        ''
    ) -join "`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath 'docs/RELEASE_MATURITY.md') -Text $maturity
    Update-ClaimsManifest -Root $Root

    $rootGit = Join-Path $Root '.git'
    [void](New-Item -ItemType Directory -Path $rootGit -Force)
    [System.IO.File]::WriteAllBytes((Join-Path $rootGit 'ignored.md'), [byte[]]@(0xEF, 0xBB, 0xBF, 0xC3, 0x28))
}

function New-TestFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Base,
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $path = Join-Path $Container $Name
    Copy-Item -LiteralPath $Base -Destination $path -Recurse -Force
    return $path
}

function Stop-ProcessTreeAndConfirm {
    param([Parameter(Mandatory = $true)][System.Diagnostics.Process]$Process)

    if ($Process.HasExited) { return $true }
    $treeKillAttempted = $false
    try {
        $killTreeMethod = $Process.GetType().GetMethod('Kill', [type[]]@([bool]))
        if ($null -ne $killTreeMethod) {
            [void]$killTreeMethod.Invoke($Process, @($true))
            $treeKillAttempted = $true
        }
    } catch {}
    if (-not $treeKillAttempted -and $script:IsWindowsPlatform) {
        try {
            $taskkillPath = Join-Path $env:SystemRoot 'System32\taskkill.exe'
            $killInfo = New-Object System.Diagnostics.ProcessStartInfo
            $killInfo.FileName = $taskkillPath
            $killInfo.Arguments = '/PID ' + $Process.Id + ' /T /F'
            $killInfo.UseShellExecute = $false
            $killInfo.CreateNoWindow = $true
            $killInfo.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
            $killer = [System.Diagnostics.Process]::Start($killInfo)
            [void]$killer.WaitForExit(5000)
            $killer.Dispose()
            $treeKillAttempted = $true
        } catch {}
    }
    if (-not $treeKillAttempted) {
        try { $Process.Kill(); $treeKillAttempted = $true } catch {}
    }
    if (-not $treeKillAttempted) { return $false }
    try { return $Process.WaitForExit(5000) } catch { return $false }
}

function Invoke-Gate {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$ScriptPath = $script:GatePath,
        [int]$TimeoutMilliseconds = 60000,
        [AllowEmptyString()][string]$GofmtOverride = $script:GofmtPath
    )

    $quotedGate = '"' + $ScriptPath.Replace('"', '\"') + '"'
    $quotedRoot = '"' + $Root.Replace('"', '\"') + '"'
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $script:PowerShellPath
    $arguments = "-NoProfile -ExecutionPolicy Bypass -File $quotedGate -Root $quotedRoot"
    if ([System.IO.Path]::GetFullPath($ScriptPath) -ceq [System.IO.Path]::GetFullPath($script:GatePath) -and
        -not [string]::IsNullOrWhiteSpace($GofmtOverride)) {
        $quotedGofmt = '"' + $GofmtOverride.Replace('"', '\"') + '"'
        $arguments += " -GofmtPath $quotedGofmt"
    }
    $startInfo.Arguments = $arguments
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    [void]$process.Start()
    $standardOutputTask = $process.StandardOutput.ReadToEndAsync()
    $standardErrorTask = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit($TimeoutMilliseconds)) {
        $stopped = Stop-ProcessTreeAndConfirm -Process $process
        if (-not $stopped) {
            return [pscustomobject]@{ ExitCode = -3; Output = @('SELFTEST_CHILD_TREE_KILL_FAILED') }
        }
        $process.WaitForExit()
        [void]$standardOutputTask.Result
        [void]$standardErrorTask.Result
        $process.Dispose()
        return [pscustomobject]@{ ExitCode = -2; Output = @('DOC_SELFTEST_CHILD_TIMEOUT') }
    }
    $process.WaitForExit()
    $standardOutput = $standardOutputTask.Result
    $standardError = $standardErrorTask.Result
    $exitCode = $process.ExitCode
    $process.Dispose()
    $output = @([System.Text.RegularExpressions.Regex]::Split(($standardOutput + $standardError).Trim(), "`r`n|`n|`r") | Where-Object { $_ -ne '' })
    return [pscustomobject]@{ ExitCode = $exitCode; Output = $output }
}

function Add-TestFailure {
    param([Parameter(Mandatory = $true)][string]$Name, [Parameter(Mandatory = $true)][string]$Code)
    $script:Failures.Add("SELFTEST_FAIL name=$Name code=$Code")
}

function Get-SafeRuleSummary {
    param([Parameter(Mandatory = $true)][object[]]$Output)
    $rules = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::Ordinal)
    foreach ($line in $Output) {
        foreach ($match in [System.Text.RegularExpressions.Regex]::Matches([string]$line, '\b(?:DOC|SELFTEST)[A-Z0-9_]+\b')) {
            [void]$rules.Add($match.Value)
        }
    }
    $array = @($rules)
    [System.Array]::Sort($array, [System.StringComparer]::Ordinal)
    if ($array.Count -eq 0) {
        return 'NO_RULE'
    }
    return (($array | Select-Object -First 5) -join '+')
}

function Assert-GatePasses {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$GofmtOverride = $script:GofmtPath
    )
    $script:CaseCount++
    $result = Invoke-Gate -Root $Root -GofmtOverride $GofmtOverride
    if ($result.ExitCode -ne 0 -or -not (($result.Output -join "`n").Contains('DOCS_GATE_OK'))) {
        Add-TestFailure -Name $Name -Code ('EXPECTED_PASS_GOT_' + (Get-SafeRuleSummary -Output $result.Output))
    }
}

function Assert-GateFailsWith {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Rule,
        [string]$ForbiddenOutput = '',
        [string]$GofmtOverride = $script:GofmtPath
    )
    $script:CaseCount++
    $result = Invoke-Gate -Root $Root -GofmtOverride $GofmtOverride
    $joined = $result.Output -join "`n"
    if ($result.ExitCode -eq 0 -or -not $joined.Contains($Rule)) {
        Add-TestFailure -Name $Name -Code ('EXPECTED_RULE_GOT_' + (Get-SafeRuleSummary -Output $result.Output))
    }
    if (-not [string]::IsNullOrEmpty($ForbiddenOutput) -and $joined.Contains($ForbiddenOutput)) {
        Add-TestFailure -Name $Name -Code 'OUTPUT_LEAK'
    }
}

function Assert-GateFailsWithRules {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string[]]$Rules,
        [string]$GofmtOverride = $script:GofmtPath
    )
    $script:CaseCount++
    $result = Invoke-Gate -Root $Root -GofmtOverride $GofmtOverride
    $joined = $result.Output -join "`n"
    if ($result.ExitCode -eq 0) {
        Add-TestFailure -Name $Name -Code 'EXPECTED_FAILURE_GOT_PASS'
        return
    }
    foreach ($rule in $Rules) {
        if (-not $joined.Contains($rule)) {
            Add-TestFailure -Name $Name -Code ('EXPECTED_' + $rule + '_GOT_' + (Get-SafeRuleSummary -Output $result.Output))
        }
    }
}

function Assert-GateRuleCount {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Rule,
        [Parameter(Mandatory = $true)][int]$MinimumCount,
        [string]$GofmtOverride = $script:GofmtPath
    )
    $script:CaseCount++
    $result = Invoke-Gate -Root $Root -GofmtOverride $GofmtOverride
    $joined = $result.Output -join "`n"
    $count = [System.Text.RegularExpressions.Regex]::Matches($joined, '(?m)^' + [System.Text.RegularExpressions.Regex]::Escape($Rule) + '\b').Count
    if ($result.ExitCode -eq 0 -or $count -lt $MinimumCount) {
        Add-TestFailure -Name $Name -Code ('EXPECTED_COUNT_GOT_' + $count)
    }
}

function Write-SortedClaimsManifest {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][object[]]$Entries
    )
    $byKey = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    $keys = New-Object 'System.Collections.Generic.List[string]'
    foreach ($entry in $Entries) {
        $key = [string]$entry.path + [char]0 + [string]$entry.line_sha256
        if ($byKey.ContainsKey($key)) {
            throw 'SELFTEST_DUPLICATE_CLAIM_ENTRY'
        }
        $byKey.Add($key, $entry)
        $keys.Add($key)
    }
    $keyArray = $keys.ToArray()
    [System.Array]::Sort($keyArray, [System.StringComparer]::Ordinal)
    $sorted = @($keyArray | ForEach-Object { $byKey[$_] })
    $manifest = [ordered]@{ schema_version = 'v1'; entries = $sorted }
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath 'testdata/release/docs-maturity-claims.v1.json') -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
}

function Add-MaturityManifestEntry {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Line,
        [Parameter(Mandatory = $true)][ValidateSet('CAPABILITY', 'POLICY')][string]$Kind,
        [object[]]$Claims = @(),
        [AllowNull()][object]$Policy = $null,
        [string]$Reason = 'fixture maturity statement'
    )
    $claimsPath = Join-FixturePath -Root $Root -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $entry = [pscustomobject][ordered]@{
        path = $Path
        line_sha256 = Get-LineSha256 -Line $Line
        kind = $Kind
        claims = @($Claims)
        policy = $Policy
        reason = $Reason
    }
    Write-SortedClaimsManifest -Root $Root -Entries @(@($manifest.entries) + $entry)
}

function New-ExternalReceiptEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$ReceiptRelative = 'testdata/release/external-evidence/fixture-cap-stable-v1.receipt.json',
        [string]$SourceUrl = 'https://evidence.freeagent.invalid/source/fixture-cap-stable-v1',
        [string]$CapabilityId = 'fixture.cap-stable',
        [string]$Result = 'pass'
    )
    $receiptObject = [ordered]@{
        schema_version = 'v1'
        capability_id = $CapabilityId
        source_url = $SourceUrl
        result = $Result
    }
    $receiptText = ($receiptObject | ConvertTo-Json -Compress) + "`n"
    $receiptPath = Join-FixturePath -Root $Root -RelativePath $ReceiptRelative
    Write-Utf8NoBom -Path $receiptPath -Text $receiptText
    return [pscustomobject][ordered]@{
        kind = 'repository-receipt-v1'
        receipt_path = $ReceiptRelative
        sha256 = Get-Sha256Hex -Bytes $script:Utf8NoBom.GetBytes($receiptText)
        source_url = $SourceUrl
    }
}

function Set-ExternalReceiptContent {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][object]$Evidence,
        [Parameter(Mandatory = $true)][object]$Receipt
    )
    $receiptText = ($Receipt | ConvertTo-Json -Compress) + "`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $Root -RelativePath ([string]$Evidence.receipt_path)) -Text $receiptText
    $Evidence.sha256 = Get-Sha256Hex -Bytes $script:Utf8NoBom.GetBytes($receiptText)
}

function Append-Utf8Line {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Line
    )
    $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($Path))
    $separator = if ($text.Length -gt 0 -and $text[$text.Length - 1] -notin @([char]10, [char]13)) { "`n" } else { '' }
    Write-Utf8NoBom -Path $Path -Text ($text + $separator + $Line + "`n")
}

function New-PrivateFixturePathText {
    param([Parameter(Mandatory = $true)][string]$Tail)
    $separator = [string][char]92
    $drivePrefix = ([string][char]67) + ([string][char]58) + $separator
    $profileSegment = 'Us' + 'ers'
    return $drivePrefix + $profileSegment + $separator + $Tail.Replace('/', $separator)
}

function ConvertFrom-CodePoints {
    param([Parameter(Mandatory = $true)][int[]]$CodePoints)
    $builder = New-Object System.Text.StringBuilder
    foreach ($codePoint in $CodePoints) {
        [void]$builder.Append([char]::ConvertFromUtf32($codePoint))
    }
    return $builder.ToString()
}

function Assert-NoReservedAutomaticVariableAssignments {
    $reserved = @(
        'IsWindows', 'IsLinux', 'IsMacOS', 'IsCoreCLR', 'PSEdition',
        'PSVersionTable', 'PID', 'Host', 'HOME', 'ShellId', 'ExecutionContext', 'PSHOME'
    )
    foreach ($path in @($script:GatePath, $PSCommandPath)) {
        $tokens = $null
        $parseErrors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$parseErrors)
        if (@($parseErrors).Count -ne 0) {
            throw 'SELFTEST_AST_PARSE_FAILED'
        }
        $assignments = $ast.FindAll({
            param($node)
            $node -is [System.Management.Automation.Language.AssignmentStatementAst] -and
            $node.Left -is [System.Management.Automation.Language.VariableExpressionAst]
        }, $true)
        foreach ($assignment in $assignments) {
            $name = $assignment.Left.VariablePath.UserPath
            if ($name.Contains(':')) {
                $name = $name.Substring($name.LastIndexOf(':') + 1)
            }
            foreach ($reservedName in $reserved) {
                if ([string]::Equals($name, $reservedName, [System.StringComparison]::OrdinalIgnoreCase)) {
                    throw 'SELFTEST_RESERVED_AUTOMATIC_VARIABLE_ASSIGNMENT'
                }
            }
        }
    }
}

if (-not (Test-Path -LiteralPath $script:GatePath -PathType Leaf)) {
    [Console]::Error.WriteLine('SELFTEST_GATE_MISSING')
    exit 1
}
Assert-NoReservedAutomaticVariableAssignments

$tempLabel = 'freeagent-docs-gate space-' + ([string][char]0x6D4B) + ([string][char]0x8BD5) + '-' + [System.Guid]::NewGuid().ToString('N')
$tempContainer = Join-Path ([System.IO.Path]::GetTempPath()) $tempLabel
$base = Join-Path $tempContainer 'base fixture'

try {
    New-BaseFixture -Root $base

    Assert-GatePasses -Name 'happy-space-nonascii-root' -Root $base

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'exact-third-party-notice-maturity-exemption'
    Write-Utf8NoBom -Path (Join-Path $case 'THIRD_PARTY_NOTICES.md') -Text "countries not thus excluded.`n"
    Assert-GatePasses -Name 'exact-root-third-party-notice-license-text-is-not-a-product-maturity-claim' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'near-match-third-party-notice-not-exempt'
    Write-Utf8NoBom -Path (Join-Path $case 'THIRD_PARTY_NOTICES-copy.md') -Text "fixture.cap-stable is stable.`n"
    Assert-GateFailsWith -Name 'third-party-notice-near-match-is-scanned' -Root $case -Rule 'DOC_MATURITY_TRIGGER_UNCLAIMED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'nested-third-party-notice-not-exempt'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'docs/THIRD_PARTY_NOTICES.md') -Text "fixture.cap-stable is stable.`n"
    Assert-GateFailsWith -Name 'nested-third-party-notice-is-scanned' -Root $case -Rule 'DOC_MATURITY_TRIGGER_UNCLAIMED'

    $appendProbe = Join-Path $tempContainer 'append-line-probe.txt'
    Write-Utf8NoBom -Path $appendProbe -Text "one`n"
    Append-Utf8Line -Path $appendProbe -Line ''
    Append-Utf8Line -Path $appendProbe -Line 'two'
    $script:CaseCount++
    $appendProbeText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($appendProbe))
    if ($appendProbeText -cne "one`n`ntwo`n") {
        Add-TestFailure -Name 'append-line-preserves-explicit-blank-line' -Code 'BYTE_SEQUENCE_MISMATCH'
    }

    $script:CaseCount++
    $relativeResult = Invoke-Gate -Root '.'
    if ($relativeResult.ExitCode -eq 0 -or -not (($relativeResult.Output -join "`n").Contains('DOC_ROOT_NOT_ABSOLUTE'))) {
        Add-TestFailure -Name 'absolute-root-required' -Code 'EXPECTED_RULE'
    }

    $script:CaseCount++
    $timeoutCase = New-TestFixture -Base $base -Container $tempContainer -Name 'child-timeout-tree'
    $timeoutScript = Join-Path $timeoutCase 'timeout-child.ps1'
    Write-Utf8NoBom -Path $timeoutScript -Text @'
[CmdletBinding()]param([Parameter(Mandatory=$true)][string]$Root)
$info = New-Object System.Diagnostics.ProcessStartInfo
$info.FileName = (Get-Process -Id $PID).Path
$info.Arguments = '-NoProfile -Command "while ($true) { Start-Sleep -Seconds 1 }"'
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
if ([System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT) {
    $info.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
}
$child = [System.Diagnostics.Process]::Start($info)
[System.IO.File]::WriteAllText((Join-Path $Root 'descendant.pid'), [string]$child.Id)
while ($true) { Start-Sleep -Seconds 1 }
'@
    $timeoutResult = Invoke-Gate -Root $timeoutCase -ScriptPath $timeoutScript -TimeoutMilliseconds 1500
    $pidPath = Join-Path $timeoutCase 'descendant.pid'
    $descendantAlive = $false
    if (Test-Path -LiteralPath $pidPath -PathType Leaf) {
        $descendantPid = [int]([System.IO.File]::ReadAllText($pidPath))
        $deadline = [System.DateTime]::UtcNow.AddSeconds(5)
        do {
            $descendantAlive = $null -ne (Get-Process -Id $descendantPid -ErrorAction SilentlyContinue)
            if ($descendantAlive) { Start-Sleep -Milliseconds 100 }
        } while ($descendantAlive -and [System.DateTime]::UtcNow -lt $deadline)
    }
    if ($timeoutResult.ExitCode -ne -2 -or -not (($timeoutResult.Output -join "`n").Contains('DOC_SELFTEST_CHILD_TIMEOUT')) -or $descendantAlive) {
        Add-TestFailure -Name 'timeout-kills-tree-and-confirms-exit' -Code 'TREE_KILL_OR_TIMEOUT_FAILED'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'broken-link'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[bad](missing.md)'
    Assert-GateFailsWith -Name 'broken-link' -Root $case -Rule 'DOC_LINK_BROKEN'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'empty-directory-link'
    [void](New-Item -ItemType Directory -Path (Join-FixturePath -Root $case -RelativePath 'docs/empty-public-directory') -Force)
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[empty directory](docs/empty-public-directory/)'
    Assert-GateFailsWith -Name 'directory-link-requires-a-public-regular-file' -Root $case -Rule 'DOC_LINK_DIRECTORY_NO_PUBLIC_FILE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-directory-link'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[historical specifications](docs/architecture/specs/)'
    Assert-GatePasses -Name 'historical-directory-link-with-public-files-passes' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'link-case-mismatch'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'docs/case-target.md') -Text "# Target`n"
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[case](docs/CASE-target.md)'
    Assert-GateFailsWith -Name 'local-link-case-must-be-exact' -Root $case -Rule 'DOC_LINK_CASE_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'local-link-reparse-first'
    $linkTarget = Join-Path $tempContainer 'local link target'
    [void](New-Item -ItemType Directory -Path $linkTarget -Force)
    Write-Utf8NoBom -Path (Join-Path $linkTarget 'NeverTraverseMarker.md') -Text "# marker`n"
    $linkPath = Join-Path $case 'linked-target'
    try {
        New-DirectoryLink -Path $linkPath -Target $linkTarget
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[linked](linked-target/NeverTraverseMarker.md)'
        Assert-GateFailsWith -Name 'local-link-reparse-rejected-before-traversal' -Root $case -Rule 'DOC_LINK_REPARSE_POINT' -ForbiddenOutput 'NeverTraverseMarker'
    } finally {
        Remove-DirectoryLink -Path $linkPath
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'strict-external-uri'
    foreach ($externalLine in @(
        '[relative](https:relative)',
        ('[userinfo](https://user:pass' + [char]0x40 + 'example.invalid/path)'),
        '[control](https://example.invalid/%0d%0aInjected)',
        '<a href="https:///missing-host">invalid</a>'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'external-uri-is-strict-and-no-userinfo-control' -Root $case -Rule 'DOC_LINK_EXTERNAL_URI_INVALID' -MinimumCount 4

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-allowed-hosts'
    foreach ($externalLine in @(
        '[markdown](https://github.com/endview/freeagent)',
        '<a href="https://docs.langchain.com/oss/python/langgraph/persistence">HTML</a>',
        '',
        'Autolink: <https://www.postgresql.org/docs/current/>',
        'Bare source: https://go.googlesource.com/tools',
        'Allowed non-default port: https://github.com:8443/endview/freeagent',
        'Visible only as code: `https://evil.invalid/not-visible`',
        'Raw tag only as code: `<a href="https://evil.invalid/not-visible-html">code</a>`',
        'Raw tag with inner tick only as double-code: ``<a title="`" href="https://evil.invalid/not-visible-html-tick">code</a>``'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GatePasses -Name 'r0-3-allows-reviewed-exact-hosts-in-visible-markdown' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-generated-third-party-legal-documents'
    Write-Utf8NoBom -Path (Join-Path $case 'THIRD_PARTY_NOTICES.md') -Text "Upstream notice: https://notice-license-host.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'third_party/npm/example/1.0.0/LICENSE.md') -Text "Upstream license: https://vendored-license-host.invalid/source`n"
    Assert-GatePasses -Name 'external-host-policy-exempts-only-canonical-generated-legal-documents' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-third-party-legal-lookalikes'
    Write-Utf8NoBom -Path (Join-Path $case 'THIRD_PARTY_NOTICES-copy.md') -Text "Near root notice: https://notice-copy.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'docs/THIRD_PARTY_NOTICES.md') -Text "Nested notice: https://nested-notice.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'third_party/npm/example/1.0.0/LICENSE-copy.md') -Text "Near license: https://license-copy.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'third_party/LICENSE.md') -Text "Noncanonical license root: https://license-root.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'licenses/npm/example/1.0.0/LICENSE.md') -Text "Outside third party: https://outside-third-party.invalid/source`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'third_party/npm/example/1.0.0/LICENSE.MD') -Text "Wrong case: https://license-case.invalid/source`n"
    Assert-GateRuleCount -Name 'external-host-policy-rejects-third-party-legal-lookalikes-and-noncanonical-paths' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -MinimumCount 6

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-host-policy'
    foreach ($externalLine in @(
        '[markdown](https://arbitrary-r0-3-policy.invalid/evidence)',
        '<a href="https://github.com.evil.invalid/path">HTML</a>',
        '<https://subdomain.github.com/path>',
        'Bare source: https://evil.invalid/path'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'r0-3-rejects-unreviewed-and-suffix-lookalike-hosts' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -MinimumCount 4

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-host-policy-bypass-forms'
    foreach ($externalLine in @(
        ('Bare userinfo: https://github.com' + [char]0x40 + 'evil.invalid/path'),
        'Bare IPv6: https://[2001:db8::1]/path',
        'Bare IDN: https://例子.测试/path',
        'Mismatched code delimiters remain visible: `https://evil.invalid/path``',
        '<a title="`" href="https://evil.invalid/raw-tag-backticks" data-x="`">raw tag</a>',
        '`prefix <bad =`> https://evil.invalid/invalid-angle-visible `',
        '<a',
        ' title="`" href="https://evil.invalid/multiline-raw-tag" data-x="`">multiline</a>',
        'prefix <!-- -- ` --> https://evil.invalid/comment-visible <!-- ` -->',
        'prefix <!a marker="`"> https://evil.invalid/declaration-visible <!b marker="`">'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'r0-3-host-policy-cannot-be-bypassed-by-authority-pseudo-code-or-raw-tag-backticks' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -MinimumCount 9

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-code-span-block-boundaries'
    foreach ($externalLine in @(
        '`multiline code starts',
        'https://evil.invalid/multiline-fails-closed',
        'and ends here`',
        '- `first-list-item',
        '- https://evil.invalid/list-item-visible`',
        '`first-paragraph',
        '',
        'https://evil.invalid/next-paragraph-visible`',
        '`plain-paragraph',
        '> https://evil.invalid/blockquote-visible`',
        '\`https://evil.invalid/escaped-backticks-visible\`'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'code-span-suppression-never-crosses-unparsed-block-boundaries' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -MinimumCount 5

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-entity-reference-fails-closed'
    $entityDestination = 'https&colon;&sol;&sol;evil&period;invalid'
    Write-Utf8NoBom -Path (Join-Path $case $entityDestination) -Text "local filename must not mask an external URI`n"
    foreach ($externalLine in @(
        ('[markdown entity](' + $entityDestination + ')'),
        ('<a href="' + $entityDestination + '">HTML named entity</a>'),
        '<a href="https&#58;//evil.invalid/numeric">HTML numeric entity</a>',
        '<a href="https&#58//evil.invalid/numeric-no-semicolon">HTML numeric entity without semicolon</a>',
        'Bare entity: https&colon;&sol;&sol;evil&period;invalid'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'link-destinations-reject-entity-reference-syntax-before-path-or-uri-resolution' -Root $case -Rule 'DOC_LINK_ENTITY_UNSUPPORTED' -MinimumCount 5

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-invalid-backtick-fence'
    foreach ($externalLine in @(
        '```bad`info',
        'https://evil.invalid/path',
        '```'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateFailsWith -Name 'invalid-backtick-fence-cannot-hide-visible-external-host' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-fence-closing-rules'
    foreach ($externalLine in @(
        '```text',
        '```not-a-closing-fence',
        'https://evil.invalid/still-code',
        '```'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GatePasses -Name 'closing-fence-with-trailing-text-does-not-expose-code-content' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-raw-html-block-pseudo-fence'
    foreach ($externalLine in @(
        '<div>',
        '```text',
        '<a href="https://evil.invalid/type-six">evil</a>',
        '```',
        '</div>',
        '',
        '<custom-block>',
        '~~~text',
        '<a href="https://evil.invalid/type-seven">evil</a>',
        '~~~',
        '</custom-block>',
        '',
        '<div>',
        ([string][char]0x00A0),
        '```text',
        '<a href="https://evil.invalid/nbsp-is-not-blank">evil</a>',
        '```',
        '</div>',
        '',
        '<pre>',
        '</pre   >',
        '~~~text',
        '<a href="https://evil.invalid/type-one-nonclosing-tag">evil</a>',
        '~~~',
        '</pre>',
        '',
        '<custom-block title="1 > 0">',
        '~~~text',
        '</custom-block>',
        '',
        'https://evil.invalid/type-seven-quoted-greater-than',
        '',
        '<!lowercase-declaration',
        '~~~text',
        '<a href="https://evil.invalid/lowercase-declaration-block">evil</a>',
        '~~~',
        '>',
        ''
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GateRuleCount -Name 'raw-html-block-content-cannot-use-pseudo-fence-to-hide-hosts' -Root $case -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -MinimumCount 6

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-uri-real-fence-precedes-html-block'
    foreach ($externalLine in @(
        '```html',
        '<div>',
        '<a href="https://evil.invalid/code-example">code example</a>',
        '</div>',
        '```'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $externalLine
    }
    Assert-GatePasses -Name 'real-fenced-code-can-contain-unreviewed-html-example' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'missing-anchor'
    $readmePath = Join-Path $case 'README.md'
    $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($readmePath)).Replace('#intro>', '#missing>')
    Write-Utf8NoBom -Path $readmePath -Text $text
    Assert-GateFailsWith -Name 'missing-anchor' -Root $case -Rule 'DOC_LINK_ANCHOR_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'root-escape'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[bad](../outside.md)'
    Assert-GateFailsWith -Name 'root-escape' -Root $case -Rule 'DOC_LINK_ROOT_ESCAPE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'space-without-angle'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[bad](docs/missing file.md)'
    Assert-GateFailsWith -Name 'space-without-angle' -Root $case -Rule 'DOC_LINK_SPACE_REQUIRES_ANGLE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'reference-link'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[bad][reference]'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '[reference]: docs/unused.md'
    Assert-GateFailsWith -Name 'reference-link-fail-closed' -Root $case -Rule 'DOC_LINK_REFERENCE_UNSUPPORTED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-local-links'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<a href="missing-a.md">broken</a>'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<img src="missing-img.bin">'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<source src=''missing-source.bin''>'
    Assert-GateRuleCount -Name 'raw-html-a-img-source' -Root $case -Rule 'DOC_LINK_BROKEN' -MinimumCount 3

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-common-url-attributes'
    $rawHtmlBrokenLines = @(
        '<video src="missing-video.bin" poster="missing-poster.bin"></video>',
        '<audio src="missing-audio.bin"></audio>',
        '<iframe src="missing-frame.md"></iframe>',
        '<link href="missing-style.bin">',
        '<script src="missing-script.bin"></script>',
        '<object data="missing-object.bin" codebase="missing-codebase.bin" archive="missing-archive-a.bin missing-archive-b.bin"></object>',
        '<embed src="missing-embed.bin">',
        '<track src="missing-track.bin">',
        '<img srcset="missing-img-a.bin 1x, missing-img-b.bin 2x">',
        '<source srcset="missing-source-a.bin 320w, missing-source-b.bin 640w">'
    )
    foreach ($rawHtmlBrokenLine in $rawHtmlBrokenLines) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $rawHtmlBrokenLine
    }
    Assert-GateRuleCount -Name 'raw-html-common-url-attributes-covered' -Root $case -Rule 'DOC_LINK_BROKEN' -MinimumCount 16

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-fail-closed'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<a href=docs/guide.md>unsafe</a>'
    Assert-GateFailsWith -Name 'raw-html-unquoted-rejected' -Root $case -Rule 'DOC_HTML_LINK_SYNTAX'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-unsupported-url-attribute'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<custom src="assets/pixel.bin"></custom>'
    Assert-GateFailsWith -Name 'raw-html-unknown-tag-url-attribute-rejected' -Root $case -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-malformed-srcset'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<img srcset="assets/pixel.bin 1q">'
    Assert-GateFailsWith -Name 'raw-html-malformed-srcset-rejected' -Root $case -Rule 'DOC_HTML_LINK_SYNTAX'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-active-content'
    foreach ($htmlLine in @(
        '<script>const releaseGateExample = true</script>',
        '<style>body { color: inherit }</style>',
        '<meta http-equiv="refresh" content="0">',
        '<img src="assets/pixel.bin" onload="releaseGateExample()">',
        '<base href="assets/pixel.bin">',
        '<link href="assets/pixel.bin">',
        '<iframe src="assets/pixel.bin"></iframe>',
        '<object data="assets/pixel.bin"></object>',
        '<embed src="assets/pixel.bin">'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GateRuleCount -Name 'raw-html-active-tags-and-event-handlers-rejected' -Root $case -Rule 'DOC_HTML_ACTIVE_CONTENT_UNSUPPORTED' -MinimumCount 9

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-browser-comment-endings'
    foreach ($htmlLine in @(
        '<!-->',
        '<script>const abruptEmptyComment = true</script>',
        '-->',
        '',
        '<!--->',
        '<style>body { color: inherit }</style>',
        '-->',
        '',
        '<!-- raw comment --!>',
        '<meta charset="utf-8">',
        '-->',
        '',
        'prefix <!--> <script>const inlineAbruptComment = true</script>'
    )) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GateRuleCount -Name 'browser-comment-endings-cannot-hide-active-raw-html' -Root $case -Rule 'DOC_HTML_ACTIVE_CONTENT_UNSUPPORTED' -MinimumCount 4

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-browser-attribute-quote-state'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<div title=bar"><script>const doubleQuoteBoundary = true</script>" >'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line '<div title=bar''><style>body { color: inherit }</style>'' >'
    Assert-GateRuleCount -Name 'unquoted-stray-quotes-cannot-hide-active-tags' -Root $case -Rule 'DOC_HTML_ACTIVE_CONTENT_UNSUPPORTED' -MinimumCount 2
    Assert-GateRuleCount -Name 'all-valued-raw-html-attributes-must-be-quoted' -Root $case -Rule 'DOC_HTML_LINK_SYNTAX' -MinimumCount 2

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-new-or-unknown-url-capability'
    $protocolRelative = ([string][char]47) + ([string][char]47) + 'unreviewed.invalid/'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line ('<link rel="preload" as="image" imagesrcset="' + $protocolRelative + 'preload.png 1x">')
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line ('<img src="assets/pixel.bin" futurefetch="' + $protocolRelative + 'future.png">')
    Assert-GateFailsWith -Name 'raw-html-imagesrcset-protocol-relative-rejected' -Root $case -Rule 'DOC_LINK_ABSOLUTE'
    Assert-GateFailsWith -Name 'raw-html-unknown-attribute-fails-closed' -Root $case -Rule 'DOC_HTML_ATTRIBUTE_UNSUPPORTED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-multiline-valid'
    foreach ($htmlLine in @('<img', ' title="1 > 0"', ' src="assets/pixel.bin"', '>', '<div title=''src="missing-decoy.bin"''>safe</div>')) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GatePasses -Name 'raw-html-multiline-and-quoted-angle-pass' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-multiline-broken'
    foreach ($htmlLine in @('<video', ' src="missing-multiline-video.bin"', ' poster="missing-multiline-poster.bin"', '>')) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GateRuleCount -Name 'raw-html-multiline-targets-checked' -Root $case -Rule 'DOC_LINK_BROKEN' -MinimumCount 2

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-multiline-unsupported'
    foreach ($htmlLine in @('<custom', ' src="assets/pixel.bin"', '>')) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GateFailsWith -Name 'raw-html-multiline-unknown-url-attribute-rejected' -Root $case -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'raw-html-unclosed-multiline'
    foreach ($htmlLine in @('<img', ' src="assets/pixel.bin')) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $htmlLine
    }
    Assert-GateFailsWith -Name 'raw-html-unclosed-tag-rejected' -Root $case -Rule 'DOC_HTML_LINK_SYNTAX'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-html-entity-obfuscation'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line 'fixture.cap-planned is st&#97;ble.'
    Assert-GateFailsWith -Name 'maturity-html-entity-split-rejected' -Root $case -Rule 'DOC_MATURITY_TEXT_OBFUSCATED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-markdown-token-split'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line 'fixture.cap-planned is st**ab**le.'
    Assert-GateFailsWith -Name 'maturity-markdown-token-split-rejected' -Root $case -Rule 'DOC_MATURITY_TEXT_OBFUSCATED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-markdown-underscore-emphasis'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line 'fixture.cap-planned is _stable_.'
    Assert-GateFailsWith -Name 'maturity-markdown-underscore-emphasis-remains-visible' -Root $case -Rule 'DOC_MATURITY_TRIGGER_UNCLAIMED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-html-quoted-angle-split'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line 'fixture.cap-planned is st<span title=">">ab</span>le.'
    Assert-GateFailsWith -Name 'maturity-visible-tokenizer-skips-quoted-angle-html-attributes' -Root $case -Rule 'DOC_MATURITY_TEXT_OBFUSCATED'

    foreach ($formatCodePoint in @(0x200B, 0xE0001, 0x180B, 0xFE0F, 0xE0100)) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('maturity-unicode-format-split-' + $formatCodePoint)
        $formatCharacter = ConvertFrom-CodePoints -CodePoints @($formatCodePoint)
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line ('fixture.cap-planned is st' + $formatCharacter + 'able.')
        Assert-GateFailsWith -Name ('maturity-unicode-format-scalar-normalized-' + $formatCodePoint) -Root $case -Rule 'DOC_MATURITY_TEXT_OBFUSCATED'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-legitimate-variation-sequences'
    $legalVariationLine = 'Rendering samples: ' +
        (ConvertFrom-CodePoints -CodePoints @(0x1820, 0x180B)) + ' ' +
        (ConvertFrom-CodePoints -CodePoints @(0x2764, 0xFE0F)) + ' ' +
        (ConvertFrom-CodePoints -CodePoints @(0x4E00, 0xE0100)) + '.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $legalVariationLine
    Assert-GatePasses -Name 'maturity-legitimate-variation-sequences-remain-valid' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-nonsemantic-regions'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line ('`stable` https://github.com/example/stable <div title="stable">safe</div> stable' + ([string][char]0x03B2))
    Assert-GatePasses -Name 'maturity-code-url-attribute-and-unicode-suffix-do-not-trigger' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-visible-link-label'
    $visibleClaimLine = '[fixture.cap-stable](docs/RELEASE_MATURITY.md#cap-stable) is **stable**.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $visibleClaimLine
    Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $visibleClaimLine -Kind 'CAPABILITY' -Claims @(
        [pscustomobject][ordered]@{ capability_id = 'fixture.cap-stable'; claimed_status = 'stable' }
    )
    Assert-GatePasses -Name 'maturity-visible-link-label-and-emphasis-pass' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-parsed-fragment-identity'
    $fragmentClaimLine = '[capability](docs/RELEASE_MATURITY.md#cap-stable) is stable.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $fragmentClaimLine
    Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $fragmentClaimLine -Kind 'CAPABILITY' -Claims @(
        [pscustomobject][ordered]@{ capability_id = 'fixture.cap-stable'; claimed_status = 'stable' }
    )
    Assert-GatePasses -Name 'maturity-identity-can-use-validated-markdown-fragment' -Root $case

    foreach ($identityAttack in @(
        [pscustomobject]@{ Name = 'id-suffix'; Line = 'fixture.cap-stable-extra is stable.' },
        [pscustomobject]@{ Name = 'title-suffix'; Line = 'Stable CapabilityX is stable.' },
        [pscustomobject]@{ Name = 'anchor-suffix'; Line = '#cap-stable-extra is stable.' },
        [pscustomobject]@{ Name = 'bare-anchor'; Line = '#cap-stable is stable.' },
        [pscustomobject]@{ Name = 'url-only'; Line = '[link](https://example.invalid/fixture.cap-stable) is stable.' },
        [pscustomobject]@{ Name = 'html-attribute'; Line = '<span title="fixture.cap-stable">safe</span> is stable.' },
        [pscustomobject]@{ Name = 'html-comment'; Line = '<!-- fixture.cap-stable --> safe is stable.' }
    )) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('maturity-identity-' + $identityAttack.Name)
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $identityAttack.Line
        Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $identityAttack.Line -Kind 'CAPABILITY' -Claims @(
            [pscustomobject][ordered]@{ capability_id = 'fixture.cap-stable'; claimed_status = 'stable' }
        )
        Assert-GateFailsWith -Name ('maturity-identity-exact-' + $identityAttack.Name) -Root $case -Rule 'DOC_MATURITY_CAPABILITY_IDENTITY_MISSING'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'utf8-bom'
    $bomTarget = Join-Path $case 'bom.md'
    $payload = $script:Utf8NoBom.GetBytes("# BOM`n")
    [System.IO.File]::WriteAllBytes($bomTarget, [byte[]](@(0xEF, 0xBB, 0xBF) + $payload))
    Assert-GateFailsWith -Name 'utf8-bom' -Root $case -Rule 'DOC_UTF8_BOM'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'utf8-invalid'
    [System.IO.File]::WriteAllBytes((Join-Path $case 'invalid.md'), [byte[]]@(0x23, 0x20, 0xC3, 0x28))
    Assert-GateFailsWith -Name 'utf8-invalid' -Root $case -Rule 'DOC_UTF8_INVALID'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'private-path'
    $secret = 'SensitivePrivateMarker'
    Write-Utf8NoBom -Path (Join-Path $case 'private.md') -Text ('private ' + (New-PrivateFixturePathText -Tail ($secret + '/workspace')) + "`n")
    Assert-GateFailsWith -Name 'private-path-redacted' -Root $case -Rule 'DOC_PRIVATE_ABSOLUTE_PATH' -ForbiddenOutput $secret

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'diagnostic-path-unicode-control'
    $unsafeDiagnosticCharacter = [string][char]0x202E
    $unsafeDiagnosticRelative = 'diagnostic-' + $unsafeDiagnosticCharacter + '.md'
    Write-Utf8NoBom -Path (Join-Path $case $unsafeDiagnosticRelative) -Text "[bad](missing.md)`n"
    $script:CaseCount++
    $unsafeDiagnosticResult = Invoke-Gate -Root $case
    $unsafeDiagnosticOutput = $unsafeDiagnosticResult.Output -join "`n"
    if ($unsafeDiagnosticResult.ExitCode -eq 0 -or
        -not $unsafeDiagnosticOutput.Contains('DOC_LINK_BROKEN') -or
        -not $unsafeDiagnosticOutput.Contains('\u202E') -or
        $unsafeDiagnosticOutput.Contains($unsafeDiagnosticCharacter)) {
        Add-TestFailure -Name 'diagnostic-path-unicode-control-is-escaped' -Code 'UNSAFE_DIAGNOSTIC_PATH'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'nested-git'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'nested/.git/must-scan.md') -Text ('private ' + (New-PrivateFixturePathText -Tail 'nested/workspace') + "`n")
    Assert-GateFailsWith -Name 'only-root-git-excluded' -Root $case -Rule 'DOC_PRIVATE_ABSOLUTE_PATH'

    $junctionRoot = Join-Path $tempContainer 'root junction'
    try {
        New-DirectoryLink -Path $junctionRoot -Target $base
        Assert-GateFailsWith -Name 'root-reparse-rejected' -Root $junctionRoot -Rule 'DOC_ROOT_REPARSE_POINT'
    } finally {
        Remove-DirectoryLink -Path $junctionRoot
    }

    $ancestorTarget = Join-Path $tempContainer 'ancestor target'
    $ancestorChild = Join-Path $ancestorTarget 'child fixture'
    [void](New-Item -ItemType Directory -Path $ancestorTarget -Force)
    Copy-Item -LiteralPath $base -Destination $ancestorChild -Recurse -Force
    $ancestorLink = Join-Path $tempContainer 'ancestor link'
    try {
        New-DirectoryLink -Path $ancestorLink -Target $ancestorTarget
        Assert-GateFailsWith -Name 'lexical-ancestor-reparse-rejected' -Root (Join-Path $ancestorLink 'child fixture') -Rule 'DOC_ROOT_REPARSE_POINT'
    } finally {
        Remove-DirectoryLink -Path $ancestorLink
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'nested-reparse'
    $junctionTarget = Join-Path $tempContainer 'junction target'
    Write-Utf8NoBom -Path (Join-Path $junctionTarget 'should-not-be-read.md') -Text ('private ' + (New-PrivateFixturePathText -Tail 'NeverReadMarker/workspace') + "`n")
    $nestedJunction = Join-Path $case 'linked'
    try {
        New-DirectoryLink -Path $nestedJunction -Target $junctionTarget
        Assert-GateFailsWith -Name 'enumeration-does-not-follow-reparse' -Root $case -Rule 'DOC_REPARSE_POINT' -ForbiddenOutput 'NeverReadMarker'
    } finally {
        Remove-DirectoryLink -Path $nestedJunction
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'fixed-release-reparse'
    $releasePath = Join-FixturePath -Root $case -RelativePath 'testdata/release'
    $releaseTarget = Join-Path $tempContainer 'release target'
    Move-Item -LiteralPath $releasePath -Destination $releaseTarget
    try {
        New-DirectoryLink -Path $releasePath -Target $releaseTarget
        Assert-GateRuleCount -Name 'matrix-and-claims-reparse-not-read' -Root $case -Rule 'DOC_FIXED_PATH_REPARSE' -MinimumCount 2
    } finally {
        Remove-DirectoryLink -Path $releasePath
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'fixed-p0-reparse'
    $specsPath = Join-FixturePath -Root $case -RelativePath 'docs/architecture/specs'
    $specsTarget = Join-Path $tempContainer 'specs target'
    Move-Item -LiteralPath $specsPath -Destination $specsTarget
    try {
        New-DirectoryLink -Path $specsPath -Target $specsTarget
        Assert-GateRuleCount -Name 'historical-spec-reparse-not-read' -Root $case -Rule 'DOC_SPEC_HISTORICAL_SOURCE_MISSING' -MinimumCount 11
    } finally {
        Remove-DirectoryLink -Path $specsPath
    }

    foreach ($fixedCase in @(
        [pscustomobject]@{ Name = 'matrix'; Relative = 'testdata/release/capabilities.v1.json'; NewName = 'Capabilities.v1.json'; Rule = 'DOC_MATURITY_MATRIX_CASE_MISMATCH' },
        [pscustomobject]@{ Name = 'maturity'; Relative = 'docs/RELEASE_MATURITY.md'; NewName = 'release_maturity.md'; Rule = 'DOC_MATURITY_DOCUMENT_CASE_MISMATCH' },
        [pscustomobject]@{ Name = 'claims'; Relative = 'testdata/release/docs-maturity-claims.v1.json'; NewName = 'Docs-maturity-claims.v1.json'; Rule = 'DOC_MATURITY_CLAIMS_CASE_MISMATCH' }
    )) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('fixed-case-' + $fixedCase.Name)
        $original = Join-FixturePath -Root $case -RelativePath $fixedCase.Relative
        $temporary = $original + '.case-temp'
        $mismatched = Join-Path ([System.IO.Path]::GetDirectoryName($original)) $fixedCase.NewName
        [System.IO.File]::Move($original, $temporary)
        [System.IO.File]::Move($temporary, $mismatched)
        Assert-GateFailsWith -Name ('fixed-path-case-exact-' + $fixedCase.Name) -Root $case -Rule $fixedCase.Rule
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'duplicate-json-key'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json') -Text '{"schema_version":"v1","schema_version":"v1","items":[]}'
    Assert-GateFailsWith -Name 'duplicate-json-key' -Root $case -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_KEY'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'nested-duplicate-json-key'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrixText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath))
    $namePattern = [System.Text.RegularExpressions.Regex]::new('"name"\s*:\s*"TestFixtureStableEvidence"')
    $matrixText = $namePattern.Replace($matrixText, '$0, "name": "TestFixtureStableEvidence"', 1)
    Write-Utf8NoBom -Path $matrixPath -Text $matrixText
    Assert-GateFailsWith -Name 'nested-duplicate-json-key' -Root $case -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_KEY'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'claims-nested-duplicate-key'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $claimsText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath))
    $statusPattern = [System.Text.RegularExpressions.Regex]::new('"claimed_status"\s*:\s*"stable"')
    $claimsText = $statusPattern.Replace($claimsText, '$0, "claimed_status": "stable"', 1)
    Write-Utf8NoBom -Path $claimsPath -Text $claimsText
    Assert-GateFailsWith -Name 'claims-nested-duplicate-key' -Root $case -Rule 'DOC_MATURITY_CLAIMS_DUPLICATE_KEY'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'claims-schema-version-type'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $manifest.schema_version = 1
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'claims-schema-version-must-be-string' -Root $case -Rule 'DOC_MATURITY_CLAIMS_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'claims-null-entry'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $manifest.entries = @(@($manifest.entries) + $null)
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'claims-null-entry-rejected' -Root $case -Rule 'DOC_MATURITY_CLAIM_ENTRY_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'claims-entry-field-type'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $manifest.entries[0].path = 123
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'claims-entry-fields-must-have-exact-types' -Root $case -Rule 'DOC_MATURITY_CLAIM_ENTRY_TYPE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'policy-null-claim-element'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $policyEntry = @($manifest.entries | Where-Object { $_.kind -ceq 'POLICY' })[0]
    $policyEntry.claims = @($null, $null)
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'policy-null-claim-elements-are-not-empty' -Root $case -Rule 'DOC_MATURITY_POLICY_HAS_CLAIMS'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'capability-claim-field-type'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $capabilityEntry = @($manifest.entries | Where-Object { $_.kind -ceq 'CAPABILITY' })[0]
    $capabilityEntry.claims[0].capability_id = $null
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'capability-claim-fields-must-have-exact-types' -Root $case -Rule 'DOC_MATURITY_CAPABILITY_CLAIM_TYPE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'matrix-item-extra'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $matrix.items[0] | Add-Member -NotePropertyName extra -NotePropertyValue 'forbidden'
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'matrix-item-extra-property' -Root $case -Rule 'DOC_MATURITY_MATRIX_ITEM_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'matrix-schema-version-type'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $matrix.schema_version = 1
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'matrix-schema-version-must-be-json-string' -Root $case -Rule 'DOC_MATURITY_MATRIX_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'matrix-item-missing'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $matrix.items[0].PSObject.Properties.Remove('owner')
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'matrix-item-missing-property' -Root $case -Rule 'DOC_MATURITY_MATRIX_ITEM_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'matrix-item-type'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $matrix.items[0].title = 123
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'matrix-item-wrong-type' -Root $case -Rule 'DOC_MATURITY_MATRIX_ITEM_TYPE'

    foreach ($statusUnderTest in @('stable', 'experimental', 'unverified', 'planned', 'excluded')) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('matrix-tests-all-status-' + $statusUnderTest)
        $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
        $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
        $targetItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-planned' })[0]
        $targetItem.status = $statusUnderTest
        $targetItem.owner = if ($statusUnderTest -ceq 'excluded') { 'excluded' } else { 'core' }
        $targetItem.tests = @(
            [pscustomobject][ordered]@{ kind = 'go'; package = './internal/core'; name = 'TestFixtureStableEvidence'; extra = 'forbidden' },
            [pscustomobject][ordered]@{ kind = 'go'; package = 123; name = 'TestFixtureStableEvidence' },
            [pscustomobject][ordered]@{ kind = 'powershell'; path = '../escape.Tests.ps1'; name = 'BadPath' },
            [pscustomobject][ordered]@{ kind = 'go'; package = './internal/core'; name = 'TestFixtureStableEvidence' },
            [pscustomobject][ordered]@{ name = 'TestFixtureStableEvidence'; kind = 'go'; package = './internal/core' }
        )
        Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
        Assert-GateFailsWithRules -Name ('matrix-tests-validated-for-' + $statusUnderTest) -Root $case -Rules @(
            'DOC_MATURITY_MATRIX_TEST_SCHEMA',
            'DOC_MATURITY_MATRIX_TEST_VALUE',
            'DOC_MATURITY_MATRIX_TEST_DUPLICATE'
        )
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'stable-test-missing'
    Remove-Item -LiteralPath (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Force
    Assert-GateFailsWith -Name 'stable-named-test-must-exist' -Root $case -Rule 'DOC_MATURITY_MATRIX_TEST_DECLARATION_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-comment-string-decoy'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
package core

/*
func TestFixtureStableEvidence(t *testing.T) {}
*/
var decoy = `
func TestFixtureStableEvidence(t *testing.T) {}
`
'@
    Assert-GateFailsWith -Name 'go-evidence-comments-and-strings-do-not-count' -Root $case -Rule 'DOC_MATURITY_MATRIX_TEST_DECLARATION_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-wrong-signature'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
package core
import "testing"
func TestFixtureStableEvidence(ctx testing.T) {}
'@
    Assert-GateFailsWith -Name 'go-evidence-requires-exact-testing-signature' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_TEST_SIGNATURE_INVALID'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-arbitrary-parameter-name'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
package core
import "testing"
func TestFixtureStableEvidence(context *testing.T) {}
'@
    Assert-GatePasses -Name 'go-evidence-accepts-any-legal-parameter-identifier' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-invalid-balanced-body'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
package core
import "testing"
func TestFixtureStableEvidence(context *testing.T) { if { } }
'@
    Assert-GateFailsWith -Name 'gofmt-parse-rejects-invalid-balanced-go-body' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_TEST_SOURCE_INVALID'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-build-constrained'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
//go:build never

package core
import "testing"
func TestFixtureStableEvidence(t *testing.T) {}
'@
    Assert-GateFailsWith -Name 'go-evidence-build-constraint-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_TEST_BUILD_CONSTRAINED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-tokenized-valid'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'internal/core/evidence_test.go') -Text @'
package core
import "testing"
func /* token comment */ TestFixtureStableEvidence ( t * testing . T ) { /* pass */ }
'@
    Assert-GatePasses -Name 'go-evidence-tokenized-top-level-signature-passes' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-path-resolution'
    $originalPath = $env:PATH
    try {
        $gofmtDirectory = [System.IO.Path]::GetDirectoryName($script:GofmtPath)
        $env:PATH = $gofmtDirectory + [System.IO.Path]::PathSeparator + $originalPath
        Assert-GatePasses -Name 'gofmt-can-be-resolved-from-inherited-path' -Root $case -GofmtOverride ''
    } finally {
        $env:PATH = $originalPath
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-repository-tool-rejected'
    $repositoryGofmtLeaf = if ($script:IsWindowsPlatform) { 'gofmt.exe' } else { 'gofmt' }
    $repositoryGofmt = Join-FixturePath -Root $case -RelativePath ('scripts/' + $repositoryGofmtLeaf)
    Write-Utf8NoBom -Path $repositoryGofmt -Text 'not an external formatter'
    Assert-GateFailsWith -Name 'repository-local-gofmt-is-not-trusted' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNSAFE' -GofmtOverride $repositoryGofmt

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-missing-tool'
    $missingGofmt = Join-Path $tempContainer ('missing-gofmt/' + $repositoryGofmtLeaf)
    Assert-GateFailsWith -Name 'missing-absolute-gofmt-fails-closed' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE' -GofmtOverride $missingGofmt

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'go-evidence-reparse-tool-rejected'
    $gofmtLinkDirectory = Join-Path $tempContainer 'gofmt-reparse-directory'
    try {
        New-DirectoryLink -Path $gofmtLinkDirectory -Target ([System.IO.Path]::GetDirectoryName($script:GofmtPath))
        $linkedGofmt = Join-Path $gofmtLinkDirectory ([System.IO.Path]::GetFileName($script:GofmtPath))
        Assert-GateFailsWith -Name 'gofmt-through-reparse-ancestor-is-not-trusted' -Root $case -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNSAFE' -GofmtOverride $linkedGofmt
    } finally {
        Remove-DirectoryLink -Path $gofmtLinkDirectory
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'powershell-evidence-valid-call'
    $powerShellEvidenceRelative = 'scripts/FixtureEvidence.Tests.ps1'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $powerShellEvidenceRelative) -Text @'
function Invoke-FixtureStableEvidence { return $true }
Invoke-FixtureStableEvidence
'@
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.tests = @([pscustomobject][ordered]@{ kind = 'powershell'; path = $powerShellEvidenceRelative; name = 'Invoke-FixtureStableEvidence' })
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GatePasses -Name 'powershell-evidence-definition-and-call-pass' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'powershell-evidence-declaration-only'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $powerShellEvidenceRelative) -Text @'
function Invoke-FixtureStableEvidence { return $true }
'@
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.tests = @([pscustomobject][ordered]@{ kind = 'powershell'; path = $powerShellEvidenceRelative; name = 'Invoke-FixtureStableEvidence' })
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'powershell-evidence-declaration-without-call-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_TEST_INVOCATION_MISSING'

    $nestedPowerShellCalls = @(
        [pscustomobject]@{ Name = 'if'; Text = 'if ($true) { Invoke-FixtureStableEvidence }' },
        [pscustomobject]@{ Name = 'loop'; Text = 'foreach ($item in @(1)) { Invoke-FixtureStableEvidence }' },
        [pscustomobject]@{ Name = 'try'; Text = 'try { Invoke-FixtureStableEvidence } catch { }' },
        [pscustomobject]@{ Name = 'function'; Text = 'function Invoke-Wrapper { Invoke-FixtureStableEvidence }' },
        [pscustomobject]@{ Name = 'scriptblock'; Text = '& { Invoke-FixtureStableEvidence }' }
    )
    foreach ($nestedPowerShellCall in $nestedPowerShellCalls) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('powershell-evidence-nested-' + $nestedPowerShellCall.Name)
        Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $powerShellEvidenceRelative) -Text (
            "function Invoke-FixtureStableEvidence { return `$true }`n" + $nestedPowerShellCall.Text + "`n"
        )
        $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
        $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
        $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
        $stableItem.tests = @([pscustomobject][ordered]@{ kind = 'powershell'; path = $powerShellEvidenceRelative; name = 'Invoke-FixtureStableEvidence' })
        Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
        Assert-GateFailsWith -Name ('powershell-evidence-nested-' + $nestedPowerShellCall.Name + '-call-rejected') -Root $case -Rule 'DOC_MATURITY_MATRIX_TEST_INVOCATION_MISSING'
    }

    $unreachablePowerShellCalls = @(
        [pscustomobject]@{ Name = 'exit'; Terminator = 'exit 0' },
        [pscustomobject]@{ Name = 'return'; Terminator = 'return' },
        [pscustomobject]@{ Name = 'throw'; Terminator = 'throw ''stop''' }
    )
    foreach ($unreachablePowerShellCall in $unreachablePowerShellCalls) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('powershell-evidence-unreachable-' + $unreachablePowerShellCall.Name)
        Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $powerShellEvidenceRelative) -Text (
            "function Invoke-FixtureStableEvidence { return `$true }`n" +
            $unreachablePowerShellCall.Terminator + "`nInvoke-FixtureStableEvidence`n"
        )
        $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
        $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
        $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
        $stableItem.tests = @([pscustomobject][ordered]@{ kind = 'powershell'; path = $powerShellEvidenceRelative; name = 'Invoke-FixtureStableEvidence' })
        Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
        Assert-GateFailsWith -Name ('powershell-evidence-call-after-' + $unreachablePowerShellCall.Name + '-rejected') -Root $case -Rule 'DOC_MATURITY_MATRIX_TEST_INVOCATION_MISSING'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'powershell-evidence-reachable-before-return'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $powerShellEvidenceRelative) -Text @'
function Invoke-FixtureStableEvidence { return $true }
Invoke-FixtureStableEvidence
return
'@
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.tests = @([pscustomobject][ordered]@{ kind = 'powershell'; path = $powerShellEvidenceRelative; name = 'Invoke-FixtureStableEvidence' })
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GatePasses -Name 'powershell-evidence-call-before-return-remains-valid' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'stable-test-empty'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.tests = @()
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'stable-requires-named-test' -Root $case -Rule 'DOC_MATURITY_MATRIX_STABLE_WITHOUT_TEST'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-stable-evidence'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-stable-requires-evidence' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-stable-valid-evidence'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $stableItem.external_evidence = @($validEvidence)
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GatePasses -Name 'external-stable-pinned-evidence-passes' -Root $case

    $receiptBindingCases = @(
        [pscustomobject]@{
            Name = 'result-fail-rehashed'
            Rule = 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_RESULT'
            Receipt = [ordered]@{ schema_version = 'v1'; capability_id = 'fixture.cap-stable'; source_url = 'https://evidence.freeagent.invalid/source/fixture-cap-stable-v1'; result = 'fail' }
        },
        [pscustomobject]@{
            Name = 'capability-mismatch-rehashed'
            Rule = 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_BINDING'
            Receipt = [ordered]@{ schema_version = 'v1'; capability_id = 'fixture.cap-other'; source_url = 'https://evidence.freeagent.invalid/source/fixture-cap-stable-v1'; result = 'pass' }
        },
        [pscustomobject]@{
            Name = 'source-mismatch-rehashed'
            Rule = 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_BINDING'
            Receipt = [ordered]@{ schema_version = 'v1'; capability_id = 'fixture.cap-stable'; source_url = 'https://other-r0-3-policy.invalid/source'; result = 'pass' }
        },
        [pscustomobject]@{
            Name = 'extra-property-rehashed'
            Rule = 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_SCHEMA'
            Receipt = [ordered]@{ schema_version = 'v1'; capability_id = 'fixture.cap-stable'; source_url = 'https://evidence.freeagent.invalid/source/fixture-cap-stable-v1'; result = 'pass'; extra = 'forbidden' }
        },
        [pscustomobject]@{
            Name = 'schema-version-type-rehashed'
            Rule = 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_SCHEMA'
            Receipt = [ordered]@{ schema_version = 1; capability_id = 'fixture.cap-stable'; source_url = 'https://evidence.freeagent.invalid/source/fixture-cap-stable-v1'; result = 'pass' }
        }
    )
    foreach ($receiptBindingCase in $receiptBindingCases) {
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('external-receipt-' + $receiptBindingCase.Name)
        $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
        $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
        $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
        $stableItem.owner = 'external-adapter'
        $validEvidence = New-ExternalReceiptEvidence -Root $case
        Set-ExternalReceiptContent -Root $case -Evidence $validEvidence -Receipt $receiptBindingCase.Receipt
        $stableItem.external_evidence = @($validEvidence)
        Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
        Assert-GateFailsWith -Name ('external-receipt-' + $receiptBindingCase.Name + '-rejected') -Root $case -Rule $receiptBindingCase.Rule
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-receipt-duplicate-json-key'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $duplicateReceiptText = '{"schema_version":"v1","capability_id":"fixture.cap-stable","source_url":"https://evidence.freeagent.invalid/source/fixture-cap-stable-v1","result":"pass","result":"pass"}' + "`n"
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $validEvidence.receipt_path) -Text $duplicateReceiptText
    $validEvidence.sha256 = Get-Sha256Hex -Bytes $script:Utf8NoBom.GetBytes($duplicateReceiptText)
    $stableItem.external_evidence = @($validEvidence)
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-receipt-duplicate-json-key-rejected-before-hash' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_JSON'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-null-evidence'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $stableItem.external_evidence = @(
        $null,
        $validEvidence
    )
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-null-evidence-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-empty-object'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $stableItem.external_evidence = @([pscustomobject]@{})
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-empty-object-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-placeholder-evidence'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $stableItem.external_evidence = @([pscustomobject][ordered]@{
        kind = 'repository-receipt-v1'
        receipt_path = '../todo.receipt.json'
        sha256 = ('0' * 64)
        source_url = 'https:relative'
    })
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-placeholder-evidence-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_VALUE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-duplicate-evidence'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $stableItem.external_evidence = @(
        $validEvidence,
        [pscustomobject][ordered]@{
            source_url = $validEvidence.source_url
            sha256 = $validEvidence.sha256
            kind = $validEvidence.kind
            receipt_path = $validEvidence.receipt_path
        }
    )
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-duplicate-canonical-evidence-rejected' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_DUPLICATE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-receipt-missing'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $stableItem.external_evidence = @([pscustomobject][ordered]@{
        kind = 'repository-receipt-v1'
        receipt_path = 'testdata/release/external-evidence/missing.receipt.json'
        sha256 = Get-LineSha256 -Line 'missing receipt bytes'
        source_url = 'https://unreachable.freeagent.invalid/source/missing'
    })
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-receipt-file-must-exist' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-receipt-hash-mismatch'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $validEvidence.sha256 = Get-LineSha256 -Line 'different receipt bytes'
    $stableItem.external_evidence = @($validEvidence)
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-receipt-raw-hash-must-match' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_HASH_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'external-receipt-case-mismatch'
    $matrixPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/capabilities.v1.json'
    $matrix = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($matrixPath)) | ConvertFrom-Json
    $stableItem = @($matrix.items | Where-Object { $_.id -ceq 'fixture.cap-stable' })[0]
    $stableItem.owner = 'external-adapter'
    $validEvidence = New-ExternalReceiptEvidence -Root $case
    $receiptOriginal = Join-FixturePath -Root $case -RelativePath $validEvidence.receipt_path
    $receiptTemporary = $receiptOriginal + '.case-temp'
    $receiptMismatched = Join-Path ([System.IO.Path]::GetDirectoryName($receiptOriginal)) 'Fixture-Cap-Stable-V1.receipt.json'
    [System.IO.File]::Move($receiptOriginal, $receiptTemporary)
    [System.IO.File]::Move($receiptTemporary, $receiptMismatched)
    $stableItem.external_evidence = @($validEvidence)
    Write-Utf8NoBom -Path $matrixPath -Text (($matrix | ConvertTo-Json -Depth 12) + "`n")
    Assert-GateFailsWith -Name 'external-receipt-case-must-be-exact' -Root $case -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_CASE_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-relocation-manifest-missing'
    Remove-Item -LiteralPath (Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative) -Force
    Assert-GateFailsWith -Name 'spec-relocation-manifest-is-required' -Root $case -Rule 'DOC_SPEC_RELOCATION_MANIFEST_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-relocation-manifest-duplicate-key'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative) -Text '{"schema_version":2,"schema_version":2,"kind":"freeagent-architecture-spec-authority-index","index":{},"current_authorities":[],"historical_sources":[]}'
    Assert-GateFailsWith -Name 'spec-relocation-manifest-rejects-duplicate-json-keys' -Root $case -Rule 'DOC_SPEC_RELOCATION_MANIFEST_JSON'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-relocation-manifest-schema-string'
    $manifestPath = Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative
    $manifestText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($manifestPath))
    $manifestText = [System.Text.RegularExpressions.Regex]::Replace($manifestText, '("schema_version"\s*:\s*)2\b', '${1}"2"', 1)
    Write-Utf8NoBom -Path $manifestPath -Text $manifestText
    Assert-GateFailsWith -Name 'spec-relocation-schema-version-rejects-string' -Root $case -Rule 'DOC_SPEC_RELOCATION_MANIFEST_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-relocation-manifest-schema-float'
    $manifestPath = Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative
    $manifestText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($manifestPath))
    $manifestText = [System.Text.RegularExpressions.Regex]::Replace($manifestText, '("schema_version"\s*:\s*)2\b', '${1}2.5', 1)
    Write-Utf8NoBom -Path $manifestPath -Text $manifestText
    Assert-GateFailsWith -Name 'spec-relocation-schema-version-rejects-float' -Root $case -Rule 'DOC_SPEC_RELOCATION_MANIFEST_SCHEMA'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-authority-manifest-format-independent'
    $manifestPath = Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($manifestPath)) | ConvertFrom-Json
    Write-Utf8NoBom -Path $manifestPath -Text (($manifest | ConvertTo-Json -Depth 8 -Compress) + "`n")
    Assert-GatePasses -Name 'spec-authority-manifest-is-semantic-not-byte-pinned' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'spec-relocation-old-path-present'
    $entry = $script:HistoricalSpecEntries[0]
    Copy-RawFile `
        -Source (Join-FixturePath -Root $case -RelativePath ('docs/architecture/specs/' + $entry.Name)) `
        -Destination (Join-FixturePath -Root $case -RelativePath ('docs/superpowers/specs/' + $entry.Name))
    Assert-GateFailsWith -Name 'spec-relocation-old-path-must-not-remain' -Root $case -Rule 'DOC_SPEC_RELOCATION_OLD_PATH_PRESENT'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-spec-missing'
    $entry = $script:HistoricalSpecEntries[0]
    Remove-Item -LiteralPath (Join-FixturePath -Root $case -RelativePath ('docs/architecture/specs/' + $entry.Name)) -Force
    Assert-GateFailsWith -Name 'declared-historical-source-must-exist' -Root $case -Rule 'DOC_SPEC_HISTORICAL_SOURCE_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-spec-extra-file'
    Write-Utf8NoBom -Path (Join-FixturePath -Root $case -RelativePath 'docs/architecture/specs/extra.md') -Text "# Extra`n"
    Assert-GateFailsWith -Name 'historical-source-directory-set-is-declared' -Root $case -Rule 'DOC_SPEC_HISTORY_SET_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-spec-crlf'
    $entry = $script:HistoricalSpecEntries[0]
    $specPath = Join-FixturePath -Root $case -RelativePath ('docs/architecture/specs/' + $entry.Name)
    $specText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($specPath)).Replace("`n", "`r`n")
    Write-Utf8NoBom -Path $specPath -Text $specText
    Assert-GateFailsWith -Name 'historical-source-rejects-crlf' -Root $case -Rule 'DOC_SPEC_LINE_ENDING_NOT_LF'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-spec-bare-cr'
    $entry = $script:HistoricalSpecEntries[0]
    $specPath = Join-FixturePath -Root $case -RelativePath ('docs/architecture/specs/' + $entry.Name)
    $specText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($specPath)).Replace("`n", "`r")
    Write-Utf8NoBom -Path $specPath -Text $specText
    Assert-GateFailsWith -Name 'historical-source-rejects-bare-cr' -Root $case -Rule 'DOC_SPEC_LINE_ENDING_NOT_LF'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'current-spec-missing'
    $entry = $script:CurrentSpecEntries[0]
    Remove-Item -LiteralPath (Join-FixturePath -Root $case -RelativePath $entry.Path) -Force
    Assert-GateFailsWith -Name 'declared-current-candidate-must-exist' -Root $case -Rule 'DOC_SPEC_CURRENT_CANDIDATE_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'current-spec-status-mismatch'
    $entry = $script:CurrentSpecEntries[0]
    $specPath = Join-FixturePath -Root $case -RelativePath $entry.Path
    $machineStatus = 'Machine status: `' + $entry.Status + '`'
    $specText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($specPath)).Replace(
        $machineStatus,
        'Machine status: `STATUS_MARKER_REMOVED`'
    )
    Write-Utf8NoBom -Path $specPath -Text $specText
    Assert-GateFailsWith -Name 'current-candidate-status-marker-is-required' -Root $case -Rule 'DOC_SPEC_STATUS_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'historical-spec-status-mismatch'
    $entry = $script:HistoricalSpecEntries[0]
    $specPath = Join-FixturePath -Root $case -RelativePath ('docs/architecture/specs/' + $entry.Name)
    $specText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($specPath)).Replace($entry.Status, 'CURRENT_NORMATIVE')
    Write-Utf8NoBom -Path $specPath -Text $specText
    Assert-GateFailsWith -Name 'historical-source-status-marker-is-required' -Root $case -Rule 'DOC_SPEC_STATUS_MISMATCH'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'authority-index-link-missing'
    $indexPath = Join-FixturePath -Root $case -RelativePath 'docs/architecture/specs/README.md'
    $indexText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($indexPath)).Replace($script:CurrentSpecEntries[0].IndexLink, '#removed-current-link')
    Write-Utf8NoBom -Path $indexPath -Text $indexText
    Assert-GateFailsWith -Name 'authority-index-must-link-current-candidates' -Root $case -Rule 'DOC_SPEC_INDEX_LINK_MISSING'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'manifest-current-status-mismatch'
    $manifestPath = Join-FixturePath -Root $case -RelativePath $script:SpecRelocationManifestRelative
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($manifestPath)) | ConvertFrom-Json
    $manifest.current_authorities[0].status = 'FROZEN'
    $manifestText = $manifest | ConvertTo-Json -Depth 8
    $manifestText = $manifestText.Replace("`r`n", "`n").Replace("`r", "`n")
    Write-Utf8NoBom -Path $manifestPath -Text ($manifestText + "`n")
    Assert-GateFailsWith -Name 'manifest-current-status-is-bounded' -Root $case -Rule 'DOC_SPEC_CURRENT_CANDIDATE_DECLARATION'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'planned-inflation'
    $readmePath = Join-Path $case 'README.md'
    $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($readmePath)).Replace('fixture.cap-planned is planned.', 'fixture.cap-planned is stable.')
    Write-Utf8NoBom -Path $readmePath -Text $text
    Update-ClaimsManifest -Root $case
    Assert-GateFailsWith -Name 'planned-cannot-be-stable' -Root $case -Rule 'DOC_MATURITY_CAPABILITY_STATUS_INFLATED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'unverified-inflation'
    $readmePath = Join-Path $case 'README.md'
    $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($readmePath)).Replace('fixture.cap-unverified is unverified.', 'fixture.cap-unverified is stable.')
    Write-Utf8NoBom -Path $readmePath -Text $text
    Update-ClaimsManifest -Root $case
    Assert-GateFailsWith -Name 'unverified-cannot-be-stable' -Root $case -Rule 'DOC_MATURITY_CAPABILITY_STATUS_INFLATED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-body-unclaimed'
    Append-Utf8Line -Path (Join-FixturePath -Root $case -RelativePath 'docs/RELEASE_MATURITY.md') -Line 'fixture.cap-stable is stable.'
    Assert-GateFailsWith -Name 'maturity-body-is-scanned' -Root $case -Rule 'DOC_MATURITY_TRIGGER_UNCLAIMED'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-anchor-single-quote-spacing'
    $maturityPath = Join-FixturePath -Root $case -RelativePath 'docs/RELEASE_MATURITY.md'
    $maturityText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($maturityPath)).Replace(
        '<a id="cap-planned"></a>',
        '<a id = ''cap-planned'' ></a>'
    )
    Write-Utf8NoBom -Path $maturityPath -Text $maturityText
    Assert-GatePasses -Name 'known-maturity-anchor-single-quote-spacing-passes' -Root $case

    $nonCanonicalAnchorCases = @(
        [pscustomobject]@{ Name = 'uppercase'; Anchor = 'Cap-Planned' },
        [pscustomobject]@{ Name = 'dot'; Anchor = 'cap.planned' },
        [pscustomobject]@{ Name = 'colon'; Anchor = 'cap:planned' }
    )
    foreach ($nonCanonicalAnchorCase in $nonCanonicalAnchorCases) {
        $nonCanonicalAnchor = $nonCanonicalAnchorCase.Anchor
        $case = New-TestFixture -Base $base -Container $tempContainer -Name ('maturity-anchor-noncanonical-' + $nonCanonicalAnchorCase.Name)
        $maturityPath = Join-FixturePath -Root $case -RelativePath 'docs/RELEASE_MATURITY.md'
        $maturityText = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($maturityPath)).Replace(
            '<a id="cap-planned"></a>',
            ('<a id="' + $nonCanonicalAnchor + '"></a>')
        )
        Write-Utf8NoBom -Path $maturityPath -Text $maturityText
        Assert-GateFailsWith -Name ('maturity-anchor-must-be-canonical-' + $nonCanonicalAnchor) -Root $case -Rule 'DOC_MATURITY_ANCHOR_SYNTAX'
    }

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'maturity-orphan-anchor-forms'
    Append-Utf8Line -Path (Join-FixturePath -Root $case -RelativePath 'docs/RELEASE_MATURITY.md') -Line '<a id = ''orphan-single'' ></a>'
    Append-Utf8Line -Path (Join-FixturePath -Root $case -RelativePath 'docs/RELEASE_MATURITY.md') -Line '<a id = "orphan-double" ></a>'
    Assert-GateRuleCount -Name 'maturity-orphan-anchor-all-supported-forms' -Root $case -Rule 'DOC_MATURITY_ORPHAN_ANCHOR' -MinimumCount 2

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'multi-capability-closure'
    $multiLine = 'fixture.cap-planned is planned; fixture.cap-unverified is unverified.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $multiLine
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $partialEntry = [pscustomobject][ordered]@{
        path = 'README.md'
        line_sha256 = Get-LineSha256 -Line $multiLine
        kind = 'CAPABILITY'
        claims = @([pscustomobject][ordered]@{ capability_id = 'fixture.cap-planned'; claimed_status = 'planned' })
        policy = $null
        reason = 'intentionally incomplete fixture claim'
    }
    Write-SortedClaimsManifest -Root $case -Entries @(@($manifest.entries) + $partialEntry)
    Assert-GateFailsWith -Name 'multi-capability-line-must-close' -Root $case -Rule 'DOC_MATURITY_CAPABILITY_CLAIMS_INCOMPLETE'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'policy-bypass'
    $readmePath = Join-Path $case 'README.md'
    $text = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($readmePath)).Replace('fixture.cap-planned is planned.', 'fixture.cap-planned is stable.')
    Write-Utf8NoBom -Path $readmePath -Text $text
    Update-ClaimsManifest -Root $case
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    foreach ($entry in $manifest.entries) {
        if ($entry.claims.Count -eq 1 -and $entry.claims[0].capability_id -ceq 'fixture.cap-planned') {
            $entry.kind = 'POLICY'
            $entry.claims = @()
            $entry.policy = [pscustomobject]@{ syntax = 'RULE'; subject = 'MATURITY' }
            $entry.reason = 'attempted policy bypass'
        }
    }
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 10) + "`n")
    Assert-GateFailsWith -Name 'policy-cannot-hide-stable-claim' -Root $case -Rule 'DOC_MATURITY_POLICY_OVERCLAIM'

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'policy-mixed-affirmative-negative-semicolon'
    $mixedPolicyLine = 'Policy: X is stable; Y must not be stable.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $mixedPolicyLine
    Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $mixedPolicyLine -Kind 'POLICY' -Policy ([pscustomobject][ordered]@{ syntax = 'RULE'; subject = 'MATURITY' })
    Assert-GateFailsWithRules -Name 'policy-mixed-semicolon-cannot-hide-positive' -Root $case -Rules @(
        'DOC_MATURITY_POLICY_POSITIVE_ASSERTION',
        'DOC_MATURITY_POLICY_OVERCLAIM'
    )

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'policy-positive-prefix-negative-suffix'
    $mixedPolicyLine = 'X is stable. Policy: Y must not be production-ready.'
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $mixedPolicyLine
    Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $mixedPolicyLine -Kind 'POLICY' -Policy ([pscustomobject][ordered]@{ syntax = 'RULE'; subject = 'MATURITY' })
    Assert-GateFailsWithRules -Name 'policy-positive-prefix-cannot-hide-in-negative-suffix' -Root $case -Rules @(
        'DOC_MATURITY_POLICY_POSITIVE_ASSERTION',
        'DOC_MATURITY_POLICY_OVERCLAIM'
    )

    $alreadyStable = ConvertFrom-CodePoints -CodePoints @(0x5DF2, 0x7A33, 0x5B9A)
    $productionUsable = ConvertFrom-CodePoints -CodePoints @(0x751F, 0x4EA7, 0x53EF, 0x7528)
    $chineseSemicolon = ConvertFrom-CodePoints -CodePoints @(0xFF1B)
    $chinesePeriod = ConvertFrom-CodePoints -CodePoints @(0x3002)
    $rulePrefix = ConvertFrom-CodePoints -CodePoints @(0x89C4, 0x5219, 0xFF1A)
    $mustNotMarkAs = ConvertFrom-CodePoints -CodePoints @(0x4E0D, 0x5F97, 0x5C06, 0x0059, 0x6807, 0x8BB0, 0x4E3A)

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'policy-mixed-chinese-english-clauses'
    $mixedChinesePolicyLine = 'X' + $alreadyStable + $chineseSemicolon + $rulePrefix + $mustNotMarkAs + $productionUsable + $chinesePeriod
    Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $mixedChinesePolicyLine
    Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $mixedChinesePolicyLine -Kind 'POLICY' -Policy ([pscustomobject][ordered]@{ syntax = 'RULE'; subject = 'MATURITY' })
    Assert-GateFailsWithRules -Name 'policy-mixed-chinese-clauses-cannot-hide-positive' -Root $case -Rules @(
        'DOC_MATURITY_POLICY_POSITIVE_ASSERTION',
        'DOC_MATURITY_POLICY_OVERCLAIM'
    )

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'chinese-policy-grammars'
    $notYet = ConvertFrom-CodePoints -CodePoints @(0x5C1A, 0x672A)
    $definitionPrefix = ConvertFrom-CodePoints -CodePoints @(0x5B9A, 0x4E49, 0xFF1A)
    $meansEvidence = ConvertFrom-CodePoints -CodePoints @(0x8868, 0x793A, 0x5177, 0x5907, 0x547D, 0x540D, 0x53EF, 0x6267, 0x884C, 0x8BC1, 0x636E)
    $mustNotMarkXAs = ConvertFrom-CodePoints -CodePoints @(0x4E0D, 0x5F97, 0x5C06, 0x0058, 0x6807, 0x8BB0, 0x4E3A)
    $validChinesePolicies = @(
        [pscustomobject]@{ Line = 'X' + $notYet + $productionUsable + $chinesePeriod; Syntax = 'NEGATION' },
        [pscustomobject]@{ Line = $definitionPrefix + $productionUsable + $meansEvidence + $chinesePeriod; Syntax = 'DEFINITION' },
        [pscustomobject]@{ Line = $rulePrefix + $mustNotMarkXAs + $productionUsable + $chinesePeriod; Syntax = 'RULE' }
    )
    foreach ($validChinesePolicy in $validChinesePolicies) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $validChinesePolicy.Line
        Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $validChinesePolicy.Line -Kind 'POLICY' -Policy ([pscustomobject][ordered]@{ syntax = $validChinesePolicy.Syntax; subject = 'MATURITY' })
    }
    Assert-GatePasses -Name 'chinese-negation-definition-rule-pass' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'chinese-capability-statuses'
    $chineseStableLine = 'fixture.cap-stable ' + $alreadyStable + $chinesePeriod
    $chineseProductionLine = 'fixture.cap-stable ' + $productionUsable + $chinesePeriod
    foreach ($chineseCapabilityLine in @($chineseStableLine, $chineseProductionLine)) {
        Append-Utf8Line -Path (Join-Path $case 'README.md') -Line $chineseCapabilityLine
        Add-MaturityManifestEntry -Root $case -Path 'README.md' -Line $chineseCapabilityLine -Kind 'CAPABILITY' -Claims @(
            [pscustomobject][ordered]@{ capability_id = 'fixture.cap-stable'; claimed_status = 'stable' }
        )
    }
    Assert-GatePasses -Name 'chinese-stable-statuses-canonicalize' -Root $case

    $case = New-TestFixture -Base $base -Container $tempContainer -Name 'stale-claim'
    $claimsPath = Join-FixturePath -Root $case -RelativePath 'testdata/release/docs-maturity-claims.v1.json'
    $manifest = $script:Utf8NoBom.GetString([System.IO.File]::ReadAllBytes($claimsPath)) | ConvertFrom-Json
    $manifest.entries[0].line_sha256 = ('0' * 64)
    Write-Utf8NoBom -Path $claimsPath -Text (($manifest | ConvertTo-Json -Depth 10) + "`n")
    Assert-GateFailsWith -Name 'stale-line-hash' -Root $case -Rule 'DOC_MATURITY_CLAIM_STALE'
} catch {
    $errorType = $_.Exception.GetType().Name
    $errorLine = $_.InvocationInfo.ScriptLineNumber
    Add-TestFailure -Name 'selftest-runtime' -Code ("UNEXPECTED_ERROR_$errorType`_LINE_$errorLine")
} finally {
    if (Test-Path -LiteralPath $tempContainer -PathType Container) {
        Remove-Item -LiteralPath $tempContainer -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $failures = $script:Failures.ToArray()
    [System.Array]::Sort($failures, [System.StringComparer]::Ordinal)
    foreach ($failure in $failures) {
        [Console]::Error.WriteLine($failure)
    }
    exit 1
}

[Console]::Out.WriteLine("DOCS_SELFTEST_OK cases=$($script:CaseCount)")
exit 0
