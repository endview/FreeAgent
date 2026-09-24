[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$gatePath = Join-Path $PSScriptRoot 'Test-PublicTree.ps1'
if (-not (Test-Path -LiteralPath $gatePath -PathType Leaf)) {
    throw 'Test-PublicTree.ps1 is missing.'
}

$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:CaseNumber = 0
$script:PassCount = 0
$script:ReparsePaths = New-Object 'System.Collections.Generic.List[string]'
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([char[]]@([char]92, [char]47))
$suiteRoot = Join-Path $tempRoot ('freeagent public tree tests 中文 ' + [Guid]::NewGuid().ToString('N'))

function Join-Codes {
    param([Parameter(Mandatory = $true)][int[]]$Codes)
    return -join @($Codes | ForEach-Object { [char]$_ })
}

function Get-RetiredConstructionScriptNames {
    return @(
        (Join-Codes @(84, 101, 115, 116, 45, 67, 111, 110, 115, 116, 114, 117, 99, 116, 105, 111, 110, 84, 114, 101, 101, 46, 112, 115, 49)),
        (Join-Codes @(84, 101, 115, 116, 45, 67, 111, 110, 115, 116, 114, 117, 99, 116, 105, 111, 110, 84, 114, 101, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49)),
        (Join-Codes @(84, 101, 115, 116, 45, 83, 101, 110, 115, 105, 116, 105, 118, 101, 66, 97, 115, 101, 108, 105, 110, 101, 46, 112, 115, 49)),
        (Join-Codes @(84, 101, 115, 116, 45, 83, 101, 110, 115, 105, 116, 105, 118, 101, 66, 97, 115, 101, 108, 105, 110, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49)),
        (Join-Codes @(98, 97, 116, 99, 104, 45, 98, 45, 114, 101, 108, 101, 97, 115, 101, 45, 103, 97, 116, 101, 46, 112, 115, 49)),
        (Join-Codes @(98, 97, 116, 99, 104, 45, 98, 45, 114, 101, 108, 101, 97, 115, 101, 45, 103, 97, 116, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49))
    )
}

function Get-RetiredBatchBArtifactNames {
    return @(
        (Join-Codes @(112, 114, 101, 45, 117, 112, 103, 114, 97, 100, 101, 45, 118, 51, 57, 46, 98, 117, 110, 100, 108, 101)),
        (Join-Codes @(99, 108, 111, 110, 101, 45, 112, 111, 115, 116, 45, 105, 109, 112, 111, 114, 116, 45, 118, 52, 48, 46, 98, 117, 110, 100, 108, 101)),
        (Join-Codes @(114, 111, 108, 108, 98, 97, 99, 107, 45, 112, 114, 111, 111, 102, 45, 118, 51, 57, 46, 98, 117, 110, 100, 108, 101)),
        (Join-Codes @(112, 114, 111, 100, 117, 99, 116, 105, 111, 110, 45, 112, 111, 115, 116, 45, 105, 109, 112, 111, 114, 116, 45, 118, 52, 48, 46, 98, 117, 110, 100, 108, 101)),
        (Join-Codes @(114, 101, 104, 101, 97, 114, 115, 97, 108, 45, 101, 118, 105, 100, 101, 110, 99, 101, 46, 106, 115, 111, 110)),
        (Join-Codes @(108, 101, 103, 97, 99, 121, 45, 99, 117, 114, 115, 111, 114, 45, 115, 116, 97, 116, 117, 115, 46, 116, 120, 116)),
        (Join-Codes @(118, 51, 57, 45, 99, 108, 111, 110, 101, 45, 114, 101, 104, 101, 97, 114, 115, 97, 108)),
        (Join-Codes @(114, 111, 108, 108, 98, 97, 99, 107, 45, 114, 101, 104, 101, 97, 114, 115, 97, 108))
    )
}

function Write-TestText {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Text
    )

    $parent = Split-Path -Parent $Path
    if ($parent) { [void][IO.Directory]::CreateDirectory($parent) }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function New-CleanFixture {
    param([Parameter(Mandatory = $true)][string]$Name)

    $script:CaseNumber++
    $safeName = [regex]::Replace($Name, '[^A-Za-z0-9_-]', '-')
    $root = Join-Path $suiteRoot (('{0:D2}-{1}' -f $script:CaseNumber, $safeName))
    [void][IO.Directory]::CreateDirectory($root)
    Write-TestText -Path (Join-Path $root 'go.mod') -Text "module github.com/endview/freeagent`n`ngo 1.22`n"
    Write-TestText -Path (Join-Path $root 'VERSION') -Text "v0.1.0-dev.1`n"
    Write-TestText -Path (Join-Path $root 'README.md') -Text "# Fixture`n`nGeneric Role, Persona, and optional modules may be discussed as extension boundaries.`n"
    Write-TestText -Path (Join-Path $root 'README.zh-CN.md') -Text "# Fixture`n`n本地化入口。`n"
    Write-TestText -Path (Join-Path $root 'README.zh-TW.md') -Text "# Fixture`n`n本地化入口。`n"
    Write-TestText -Path (Join-Path $root 'cmd/main.go') -Text "package main`n`nfunc main() {}`n"
    $sha = '1' * 40
    Write-TestText -Path (Join-Path $root '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  test:`n    runs-on: ubuntu-latest`n    steps:`n      - uses: actions/checkout@$sha`n"
    return $root
}

function Get-PowerShellEngine {
    if ($PSVersionTable.PSEdition -eq 'Desktop') {
        return (Join-Path $PSHOME 'powershell.exe')
    }
    $name = if ($env:OS -eq 'Windows_NT') { 'pwsh.exe' } else { 'pwsh' }
    return (Join-Path $PSHOME $name)
}

function Invoke-PublicTreeGate {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$ScriptPath = $gatePath,
        [int]$TimeoutMilliseconds = 30000
    )

    if ($ScriptPath.Contains('"') -or $Root.Contains('"')) {
        throw 'Test harness paths must not contain a quote.'
    }
    if ($TimeoutMilliseconds -lt 100) { throw 'Test harness timeout is too short.' }
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = Get-PowerShellEngine
    $startInfo.Arguments = '-NoLogo -NoProfile -NonInteractive -File "' + $ScriptPath + '" -Root "' + $Root + '"'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $startInfo
    if (-not $process.Start()) { throw 'Unable to start PowerShell test child process.' }
    try {
        $processId = $process.Id
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timedOut = -not $process.WaitForExit($TimeoutMilliseconds)
        if ($timedOut) {
            try { $process.Kill() } catch { }
            if (-not $process.WaitForExit(5000)) {
                throw 'Unable to terminate timed-out PowerShell test child process.'
            }
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        $result = [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdout
            Stderr = $stderr
            Output = $stdout + $stderr
            TimedOut = $timedOut
            StillRunning = -not $process.HasExited
            ProcessId = $processId
        }
    } finally {
        $process.Dispose()
    }
    return $result
}

function Assert-GatePasses {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $result = Invoke-PublicTreeGate -Root $Root
    if ($result.TimedOut -or $result.StillRunning -or $result.ExitCode -ne 0 -or $result.Stderr.Length -ne 0 -or $result.Output.IndexOf('PUBLIC_TREE_PASS', [StringComparison]::Ordinal) -lt 0) {
        throw "PASS assertion failed [$Name]: exit=$($result.ExitCode)`n$($result.Output)"
    }
    $script:PassCount++
}

function Assert-GateFails {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Rule,
        [string[]]$ProtectedValues = @(),
        [string[]]$RequiredOutput = @()
    )

    $result = Invoke-PublicTreeGate -Root $Root
    if ($result.TimedOut -or $result.StillRunning) {
        throw "FAIL assertion timed out [$Name]."
    }
    if ($result.ExitCode -eq 0) {
        throw "FAIL assertion unexpectedly passed [$Name]."
    }
    if ($result.Stderr.Length -ne 0) {
        throw "FAIL assertion wrote to stderr [$Name]."
    }
    $needle = 'rule=' + $Rule
    if ($result.Output.IndexOf($needle, [StringComparison]::Ordinal) -lt 0) {
        throw "FAIL assertion reported the wrong rule [$Name], expected=$Rule`n$($result.Output)"
    }
    foreach ($protected in $ProtectedValues) {
        if ($protected -and $result.Output.IndexOf($protected, [StringComparison]::Ordinal) -ge 0) {
            throw "FAIL assertion leaked protected content [$Name]."
        }
    }
    foreach ($required in $RequiredOutput) {
        if ($required -and $result.Output.IndexOf($required, [StringComparison]::Ordinal) -lt 0) {
            throw "FAIL assertion omitted required safe output [$Name]: $required"
        }
    }
    $script:PassCount++
}

function New-ReparseDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Target
    )

    if ($env:OS -eq 'Windows_NT') {
        [void](New-Item -ItemType Junction -Path $Path -Target $Target -Force)
    } else {
        [void](New-Item -ItemType SymbolicLink -Path $Path -Target $Target -Force)
    }
    $script:ReparsePaths.Add($Path)
}

function Try-MarkSparseFile {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($env:OS -ne 'Windows_NT') { return $false }
    if (-not ('PublicTreeSparseFixture' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

public static class PublicTreeSparseFixture
{
    private const uint FsctlSetSparse = 0x000900c4;

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DeviceIoControl(
        SafeFileHandle device,
        uint controlCode,
        IntPtr inputBuffer,
        int inputBufferSize,
        IntPtr outputBuffer,
        int outputBufferSize,
        out int bytesReturned,
        IntPtr overlapped);

    public static bool Mark(string path)
    {
        using (var stream = new FileStream(path, FileMode.Open, FileAccess.ReadWrite, FileShare.ReadWrite | FileShare.Delete))
        {
            int bytesReturned;
            return DeviceIoControl(
                stream.SafeFileHandle,
                FsctlSetSparse,
                IntPtr.Zero,
                0,
                IntPtr.Zero,
                0,
                out bytesReturned,
                IntPtr.Zero);
        }
    }
}
'@ -ErrorAction Stop
    }
    return [PublicTreeSparseFixture]::Mark($Path)
}

try {
    [void][IO.Directory]::CreateDirectory($suiteRoot)

    $gateSource = [IO.File]::ReadAllText($gatePath)
    $negativeSplitCountPattern = '(?im)-split[^\r\n]*,\s*-\d+'
    if ([regex]::IsMatch($gateSource, $negativeSplitCountPattern)) {
        throw 'Portable line splitting regression: negative -split count is version-dependent.'
    }
    $script:PassCount++

    $parseTokens = $null
    $parseErrors = $null
    $gateAst = [Management.Automation.Language.Parser]::ParseInput($gateSource, [ref]$parseTokens, [ref]$parseErrors)
    if ($parseErrors.Count -ne 0) { throw 'Gate source has a PowerShell parse error.' }
    $safePathFunctions = @($gateAst.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq 'ConvertTo-SafeDisplayPath'
    }, $true))
    if ($safePathFunctions.Count -ne 1) { throw 'Safe display path function could not be isolated for unit testing.' }
    $safePathResult = & {
        param([string]$FunctionText)
        $contentRules = @()
        function Get-RuleTextForms {
            param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)
            Write-Output $Text
        }
        function Test-CredentialAssignmentSecret {
            param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)
            return $false
        }
        . ([scriptblock]::Create($FunctionText))
        $invalid = 'left' + [char]0xd800 + 'middle' + [char]0xdc00 + '.md'
        $validAstral = [char]::ConvertFromUtf32(0x1f600)
        return [pscustomobject]@{
            Invalid = ConvertTo-SafeDisplayPath -Path $invalid -Rule 'PT_RUNTIME'
            Valid = ConvertTo-SafeDisplayPath -Path ('ok' + $validAstral + '.md') -Rule 'PT_RUNTIME'
            ValidExpected = 'ok' + $validAstral + '.md'
        }
    } $safePathFunctions[0].Extent.Text
    if (
        $safePathResult.Invalid.IndexOf('\ud800', [StringComparison]::Ordinal) -lt 0 -or
        $safePathResult.Invalid.IndexOf('\udc00', [StringComparison]::Ordinal) -lt 0 -or
        $safePathResult.Invalid.IndexOf([string][char]0xd800, [StringComparison]::Ordinal) -ge 0 -or
        $safePathResult.Invalid.IndexOf([string][char]0xdc00, [StringComparison]::Ordinal) -ge 0 -or
        $safePathResult.Valid -cne $safePathResult.ValidExpected
    ) {
        throw 'Safe display path surrogate escaping regression.'
    }
    $script:PassCount++

    $clean = New-CleanFixture -Name 'clean root with spaces and 中文'
    $ignoredValue = (Join-Codes @(115, 107, 45)) + ('Z' * 24)
    Write-TestText -Path (Join-Path $clean '.git/ignored.txt') -Text $ignoredValue
    Write-TestText -Path (Join-Path $clean 'third_party/NOTICE.txt') -Text "audited third-party notice`n"
    Write-TestText -Path (Join-Path $clean 'vendor/modules.txt') -Text "audited vendor metadata`n"
    Write-TestText -Path (Join-Path $clean 'testdata/data/fixture.txt') -Text "nested source fixture`n"
    $embeddedShort = 'A' + (Join-Codes @(120,120,98)) + 'B'
    $embeddedOwner = 'A' + (Join-Codes @(99,104,117,110,107,98,117,114,115,116)) + 'B'
    Write-TestText -Path (Join-Path $clean 'docs/boundaries.md') -Text ($embeddedShort + "`n" + $embeddedOwner + "`n")
    Write-TestText -Path (Join-Path $clean 'docs/license-identifier.ps1') -Text "LicenseIdentifier='AGPL-3.0-only'`n"
    Write-TestText -Path (Join-Path $clean 'docs/auth-language.md') -Text "Authorization-sk-example-secret-private`nBearer authentication`nBearer redacted`n"
    foreach ($frontendArtifact in @('node_modules', '.npm-cache', '.vite')) {
        Write-TestText -Path (Join-Path $clean ('internal/controlweb/' + $frontendArtifact + '/fixture.txt')) -Text 'token=not-a-public-secret'
    }
    Write-TestText -Path (Join-Path $clean '.github/actions/local/action.yml') -Text "name: local`nruns:`n  using: composite`n  steps:`n    - shell: bash`n      run: echo ok`n"
    $sha2 = '2' * 40
    Write-TestText -Path (Join-Path $clean '.github/workflows/reusable.yml') -Text "name: reusable`non:`n  workflow_call:`njobs:`n  noop:`n    runs-on: ubuntu-latest`n    steps:`n      - run: echo ok`n"
    Write-TestText -Path (Join-Path $clean '.github/workflows/local.yml') -Text "name: local`non: [push]`njobs:`n  steps:`n    runs-on: ubuntu-latest`n    steps:`n      - 'uses': './.github/actions/local'`n      - `"uses`": `"actions/setup-go@$sha2`"`n      - run: |`n          echo 'uses: actions/example@v4'`n  local-reusable:`n    uses: ./.github/workflows/reusable.yml`n  remote:`n    uses: 'owner/repository/.github/workflows/reusable.yml@$sha2' # audited`n"
    Assert-GatePasses -Name 'portable clean fixture and exact root git exclusion' -Root $clean

    $missingVersion = New-CleanFixture -Name 'missing release version'
    Remove-Item -LiteralPath (Join-Path $missingVersion 'VERSION') -Force
    Assert-GateFails `
        -Name 'VERSION is mandatory' `
        -Root $missingVersion `
        -Rule 'PT_VERSION_MISSING'

    foreach ($invalidVersion in @(
        '0.1.0-dev.1',
        'v01.0.0',
        'v0.1.0-dev.01',
        "v0.1.0-dev.1`r`n",
        "v0.1.0-dev.1`nextra`n"
    )) {
        $invalidVersionFixture = New-CleanFixture `
            -Name ('invalid release version ' + [Guid]::NewGuid().ToString('N'))
        Write-TestText `
            -Path (Join-Path $invalidVersionFixture 'VERSION') `
            -Text $invalidVersion
        Assert-GateFails `
            -Name 'VERSION requires one canonical v-prefixed SemVer LF line' `
            -Root $invalidVersionFixture `
            -Rule 'PT_VERSION_INVALID'
    }

    $blockingScript = Join-Path $suiteRoot 'blocking-gate.ps1'
    Write-TestText -Path $blockingScript -Text "param([string]`$Root)`nwhile (`$true) { Start-Sleep -Seconds 30 }`n"
    $timeoutResult = Invoke-PublicTreeGate -Root $clean -ScriptPath $blockingScript -TimeoutMilliseconds 500
    if (-not $timeoutResult.TimedOut -or $timeoutResult.StillRunning) {
        throw 'Test harness did not terminate a timed-out child process.'
    }
    $script:PassCount++

    $selfScan = New-CleanFixture -Name 'gate self scan'
    [void][IO.Directory]::CreateDirectory((Join-Path $selfScan 'scripts'))
    [IO.File]::WriteAllBytes((Join-Path $selfScan 'scripts/Test-PublicTree.ps1'), [IO.File]::ReadAllBytes($gatePath))
    [IO.File]::WriteAllBytes((Join-Path $selfScan 'scripts/Test-PublicTree.Tests.ps1'), [IO.File]::ReadAllBytes($PSCommandPath))
    Assert-GatePasses -Name 'gate and self-test do not self-trigger' -Root $selfScan

    Assert-GateFails -Name 'relative root' -Root '.' -Rule 'PT_ROOT_ABSOLUTE'
    Assert-GateFails -Name 'missing root' -Root (Join-Path $suiteRoot 'missing') -Rule 'PT_ROOT_MISSING'
    $rootFile = Join-Path $suiteRoot 'root-file.txt'
    Write-TestText -Path $rootFile -Text 'not a directory'
    Assert-GateFails -Name 'file root' -Root $rootFile -Rule 'PT_ROOT_NOT_DIRECTORY'
    $filesystemRootArgument = [IO.Path]::GetPathRoot($suiteRoot) + '.'
    Assert-GateFails -Name 'filesystem root is canonicalized and rejected before traversal' -Root $filesystemRootArgument -Rule 'PT_ROOT_FILESYSTEM'

    $unknown = New-CleanFixture -Name 'unknown top level'
    Write-TestText -Path (Join-Path $unknown 'unknown.txt') -Text 'x'
    Assert-GateFails -Name 'strict top level allowlist' -Root $unknown -Rule 'PT_TOP_LEVEL_NOT_ALLOWED'

    $nestedGit = New-CleanFixture -Name 'nested git'
    Write-TestText -Path (Join-Path $nestedGit 'docs/.git/config') -Text 'x'
    Assert-GateFails -Name 'nested git is not excluded' -Root $nestedGit -Rule 'PT_CONSTRUCTION'

    $sensitiveCases = @(
        [pscustomobject]@{ Name = 'private-key'; Rule = 'PT_SECRET_PRIVATE_KEY'; Value = '-----BEGIN ' + (Join-Codes @(80,82,73,86,65,84,69,32,75,69,89)) + '-----' },
        [pscustomobject]@{ Name = 'provider-key'; Rule = 'PT_SECRET_PROVIDER_KEY'; Value = (Join-Codes @(115,107,45)) + ('A' * 24) },
        [pscustomobject]@{ Name = 'github-token'; Rule = 'PT_SECRET_GITHUB_TOKEN'; Value = (Join-Codes @(103,104,112,95)) + ('B' * 24) },
        [pscustomobject]@{ Name = 'slack-token'; Rule = 'PT_SECRET_SLACK_TOKEN'; Value = (Join-Codes @(120,111,120,98,45)) + ('C' * 20) },
        [pscustomobject]@{ Name = 'aws-key'; Rule = 'PT_SECRET_AWS_KEY'; Value = (Join-Codes @(65,75,73,65)) + ('D' * 16) },
        [pscustomobject]@{ Name = 'bearer'; Rule = 'PT_SECRET_BEARER'; Value = (Join-Codes @(66,101,97,114,101,114,32)) + ('E' * 20) },
        [pscustomobject]@{ Name = 'assignment'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = 'api_' + 'key="' + ('F' * 20) + '"' },
        [pscustomobject]@{ Name = 'unquoted-assignment'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = 'token=' + ('A1' * 10) },
        [pscustomobject]@{ Name = 'unquoted-alpha-assignment'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = 'token=' + ('Q' * 20) },
        [pscustomobject]@{ Name = 'uri-userinfo'; Rule = 'PT_SECRET_URI_USERINFO'; Value = 'https://' + 'user:' + ('G' * 16) + '@example.invalid/path' },
        [pscustomobject]@{ Name = 'windows-private'; Rule = 'PT_PRIVATE_WINDOWS_PATH'; Value = 'C:' + [char]92 + 'Users' + [char]92 + 'private-user' + [char]92 + 'file.txt' },
        [pscustomobject]@{ Name = 'posix-private'; Rule = 'PT_PRIVATE_POSIX_HOME_PATH'; Value = '/home/' + 'private-user/file.txt' },
        [pscustomobject]@{ Name = 'posix-local'; Rule = 'PT_PRIVATE_POSIX_LOCAL_PATH'; Value = '/tmp/' + 'private-build/output.txt' }
    )
    $awsFieldName = Join-Codes @(65, 87, 83, 95, 83, 69, 67, 82, 69, 84, 95, 65, 67, 67, 69, 83, 83, 95, 75, 69, 89)
    $credentialFieldName = Join-Codes @(112, 97, 115, 115, 119, 111, 114, 100)
    $sensitiveCases += [pscustomobject]@{ Name = 'full variable name short credential'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $awsFieldName + '=short' }
    $sensitiveCases += [pscustomobject]@{ Name = 'full variable name license-like credential'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $awsFieldName + '=MIT' }
    $sensitiveCases += [pscustomobject]@{ Name = 'single character password'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '=x' }
    $sensitiveCases += [pscustomobject]@{ Name = 'quoted credential containing spaces'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '="two words"' }
    $sensitiveCases += [pscustomobject]@{ Name = 'license identifier is not a credential placeholder'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '=MIT' }
    $sensitiveCases += [pscustomobject]@{ Name = 'plain token license identifier is not exempt'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = (Join-Codes @(116, 111, 107, 101, 110)) + '=MIT' }
    $sensitiveCases += [pscustomobject]@{ Name = 'valid is not a credential placeholder'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = (Join-Codes @(116, 111, 107, 101, 110)) + '=valid' }
    $sensitiveCases += [pscustomobject]@{ Name = 'invalid is not a credential placeholder'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '=invalid' }
    $sensitiveCases += [pscustomobject]@{ Name = 'placeholder prefix with adjacent suffix'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '="placeholder"suffix' }
    $sensitiveCases += [pscustomobject]@{ Name = 'placeholder prefix with expression suffix'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '="placeholder" + actual' }
    $sensitiveCases += [pscustomobject]@{ Name = 'provider project key'; Rule = 'PT_SECRET_PROVIDER_KEY'; Value = (Join-Codes @(115, 107, 45, 112, 114, 111, 106, 45)) + ('P' * 24) }
    $sensitiveCases += [pscustomobject]@{ Name = 'realistic deepseek provider key'; Rule = 'PT_SECRET_PROVIDER_KEY'; Value = (Join-Codes @(115, 107, 45)) + ('a1B2' * 8) }
    $sensitiveCases += [pscustomobject]@{ Name = 'realistic bearer value'; Rule = 'PT_SECRET_BEARER'; Value = (Join-Codes @(66, 101, 97, 114, 101, 114, 32)) + ('Ab3_' * 6) }
    $sensitiveCases += [pscustomobject]@{ Name = 'hardcoded password assignment'; Rule = 'PT_SECRET_ASSIGNMENT'; Value = $credentialFieldName + '="correct-horse-battery-staple"' }
    $sensitiveCases += [pscustomobject]@{ Name = 'wsl-unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(92,92)) + 'wsl$' + (Join-Codes @(92)) + 'Ubuntu' + (Join-Codes @(92)) + 'home' }
    $sensitiveCases += [pscustomobject]@{ Name = 'extended-unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(92,92,63,92)) + 'UNC' + (Join-Codes @(92)) + 'server' + (Join-Codes @(92)) + 'share' + (Join-Codes @(92)) + 'folder' }
    $sensitiveCases += [pscustomobject]@{ Name = 'backslash unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(92,92)) + 'server' + (Join-Codes @(92)) + 'share/folder' }
    $sensitiveCases += [pscustomobject]@{ Name = 'slash unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(47,47)) + 'server/share/folder' }
    $sensitiveCases += [pscustomobject]@{ Name = 'mixed backslash unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(92,92)) + 'server/share/folder' }
    $sensitiveCases += [pscustomobject]@{ Name = 'mixed slash unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(47,47)) + 'server' + (Join-Codes @(92)) + 'share/folder' }
    $sensitiveCases += [pscustomobject]@{ Name = 'extended slash unc'; Rule = 'PT_PRIVATE_UNC_PATH'; Value = (Join-Codes @(47,47,63,47)) + 'UNC/server/share/folder' }
    foreach ($case in $sensitiveCases) {
        $root = New-CleanFixture -Name $case.Name
        Write-TestText -Path (Join-Path $root 'docs/input.md') -Text ([string]$case.Value)
        Assert-GateFails -Name $case.Name -Root $root -Rule $case.Rule -ProtectedValues @([string]$case.Value)
    }

    $javascriptRegexLiterals = New-CleanFixture -Name 'javascript regex literals are not unc paths'
    Write-TestText -Path (Join-Path $javascriptRegexLiterals 'internal/fixture/safe.mjs') -Text (
        "const normalize = /\r\n?/g;`nconst forbidden = /\bEventSource\b/u;`n"
    )
    Assert-GatePasses -Name 'JavaScript regex escape sequences do not become UNC paths' -Root $javascriptRegexLiterals

    $reviewedUncLineNumber = 153
    $reviewedUncText =
        (Join-Codes @(9, 9, 96, 92, 92)) + 'server' +
        (Join-Codes @(92)) + 'share' + (Join-Codes @(92)) + 'file' +
        (Join-Codes @(96, 44))
    $reviewedUncPrefix = "`n" * ($reviewedUncLineNumber - 1)

    $reviewedUncLine = New-CleanFixture -Name 'reviewed UNC line'
    Write-TestText -Path (Join-Path $reviewedUncLine 'sdk/moduleapi/artifact_test.go') -Text (
        $reviewedUncPrefix + $reviewedUncText + "`n"
    )
    Assert-GatePasses -Name 'reviewed UNC line requires exact path line and bytes' -Root $reviewedUncLine

    $reviewedUncWrongPath = New-CleanFixture -Name 'reviewed UNC line wrong path'
    Write-TestText -Path (Join-Path $reviewedUncWrongPath 'internal/fixture/artifact_test.go') -Text (
        $reviewedUncPrefix + $reviewedUncText + "`n"
    )
    Assert-GateFails -Name 'reviewed UNC line is path scoped' -Root $reviewedUncWrongPath -Rule 'PT_PRIVATE_UNC_PATH'

    $reviewedUncWrongLine = New-CleanFixture -Name 'reviewed UNC line wrong line'
    Write-TestText -Path (Join-Path $reviewedUncWrongLine 'sdk/moduleapi/artifact_test.go') -Text (
        "`n" + $reviewedUncPrefix + $reviewedUncText + "`n"
    )
    Assert-GateFails -Name 'reviewed UNC line is line scoped' -Root $reviewedUncWrongLine -Rule 'PT_PRIVATE_UNC_PATH'

    $reviewedUncWrongText = New-CleanFixture -Name 'reviewed UNC line wrong text'
    Write-TestText -Path (Join-Path $reviewedUncWrongText 'sdk/moduleapi/artifact_test.go') -Text (
        $reviewedUncPrefix + $reviewedUncText + ' ' + "`n"
    )
    Assert-GateFails -Name 'reviewed UNC line is byte exact' -Root $reviewedUncWrongText -Rule 'PT_PRIVATE_UNC_PATH'

    $reviewedUncDuplicate = New-CleanFixture -Name 'reviewed UNC line duplicate'
    Write-TestText -Path (Join-Path $reviewedUncDuplicate 'sdk/moduleapi/artifact_test.go') -Text (
        $reviewedUncPrefix + $reviewedUncText + "`n" + $reviewedUncText + "`n"
    )
    Assert-GateFails -Name 'reviewed UNC line may occur only at its reviewed occurrence' -Root $reviewedUncDuplicate -Rule 'PT_PRIVATE_UNC_PATH'

    $credentialBoundaries = New-CleanFixture -Name 'credential variable boundaries and placeholders'
    $credentialBoundaryText = "secretary=ordinary`ntokenizer=ordinary`nauthorization_result=VALID`nauthorization_result=invalid`n" + $awsFieldName + "=placeholder`n" + $credentialFieldName + "=`n" + $credentialFieldName + '=${' + $awsFieldName + "}`n" + $credentialFieldName + '={{ ' + $awsFieldName + " }}`n" + $credentialFieldName + "=`"placeholder`"`n"
    Write-TestText -Path (Join-Path $credentialBoundaries 'docs/credentials.env.example') -Text $credentialBoundaryText
    Assert-GatePasses -Name 'credential scanner uses complete variable components and permits placeholders or empty values' -Root $credentialBoundaries

    $nearSentinel = New-CleanFixture -Name 'credential sentinel near matches'
    $nearSentinelText =
        $awsFieldName + "=SensitivePrivateMarkerX`n" +
        $credentialFieldName + "=NeverReadMarker-suffix`n"
    Write-TestText -Path (Join-Path $nearSentinel 'docs/credentials.env') -Text $nearSentinelText
    Assert-GateFails -Name 'credential sentinel near matches are not exempt' -Root $nearSentinel -Rule 'PT_SECRET_ASSIGNMENT'

    $fieldWordA = Join-Codes @(116, 111, 107, 101, 110)
    $fieldWordB = Join-Codes @(97, 112, 105, 95, 107, 101, 121)
    $goCredentialSyntax = New-CleanFixture -Name 'go credential syntax safe forms'
    $goCredentialSafeText =
        "package fixture`n`nimport `"os`"`n`ntype Event struct { Token string; APIKey string }`ntype Holder struct { Token string; APIKey string }`n" +
        "func safe(event Event, token string) Holder {`n" +
        "  dynamic := event." + (Join-Codes @(84, 111, 107, 101, 110)) + "`n" +
        "  if " + $fieldWordA + " == `"`" || " + $fieldWordA + " != dynamic {}`n" +
        "  // " + (Join-Codes @(84, 111, 107, 101, 110)) + ": `"comment-only`"`n" +
        "  /*`n  " + $fieldWordA + " := `"block-comment-only`"`n  */`n" +
        "  return Holder{" + (Join-Codes @(84, 111, 107, 101, 110)) + ": " + $fieldWordA + ", " +
        (Join-Codes @(65, 80, 73, 75, 101, 121)) + ": event.APIKey}`n" +
        "}`n" +
        "func fromEnv() string { runtimeValue := os.Getenv(`"MODEL_TOKEN`"); return runtimeValue }`n"
    Write-TestText -Path (Join-Path $goCredentialSyntax 'internal/fixture/safe.go') -Text $goCredentialSafeText
    Assert-GatePasses -Name 'Go credential syntax permits dynamic fields comparisons comments SecretRef and env reads' -Root $goCredentialSyntax

    $csrfFieldName = Join-Codes @(99, 115, 114, 102, 84, 111, 107, 101, 110)
    $skipName = Join-Codes @(115, 107, 105, 112, 84, 111, 107, 101, 110)
    $reviewedJavaScriptLine = New-CleanFixture -Name 'reviewed JavaScript credential line'
    Write-TestText -Path (Join-Path $reviewedJavaScriptLine 'internal/controlweb/src/app.tsx') -Text (
        '  ' + $csrfFieldName + ": string;`n"
    )
    Assert-GatePasses -Name 'reviewed JavaScript credential line requires exact path and bytes' -Root $reviewedJavaScriptLine

    $reviewedJavaScriptDuplicate = New-CleanFixture -Name 'reviewed JavaScript credential line duplicate'
    Write-TestText -Path (Join-Path $reviewedJavaScriptDuplicate 'internal/controlweb/src/app.tsx') -Text (
        ('  ' + $csrfFieldName + ": string;`n") * 2
    )
    Assert-GateFails -Name 'reviewed JavaScript credential line may occur at most once' -Root $reviewedJavaScriptDuplicate -Rule 'PT_SECRET_ASSIGNMENT'

    $reviewedJavaScriptWrongPath = New-CleanFixture -Name 'reviewed JavaScript credential line wrong path'
    Write-TestText -Path (Join-Path $reviewedJavaScriptWrongPath 'internal/fixture/app.tsx') -Text (
        '  ' + $csrfFieldName + ": string;`n"
    )
    Assert-GateFails -Name 'reviewed JavaScript credential line is path scoped' -Root $reviewedJavaScriptWrongPath -Rule 'PT_SECRET_ASSIGNMENT'

    $reviewedJavaScriptNearMiss = New-CleanFixture -Name 'reviewed JavaScript credential line near miss'
    Write-TestText -Path (Join-Path $reviewedJavaScriptNearMiss 'internal/controlweb/src/app.tsx') -Text (
        ' ' + $csrfFieldName + ": string;`n"
    )
    Assert-GateFails -Name 'reviewed JavaScript credential line is byte exact' -Root $reviewedJavaScriptNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $reviewedVendorLine = New-CleanFixture -Name 'reviewed generated vendor line'
    Write-TestText -Path (Join-Path $reviewedVendorLine 'internal/controlweb/dist/assets/tanstack-query.js') -Text (
        'var ' + $skipName + " = /* @__PURE__ */ Symbol();`n"
    )
    Assert-GatePasses -Name 'reviewed generated vendor line requires exact path and bytes' -Root $reviewedVendorLine

    $goOrdinaryNames = New-CleanFixture -Name 'go ordinary lexical names'
    $goOrdinaryNamesText =
        "package fixture`n`nfunc safe(tokenizer string, token_budget int, promptTokenCount int) int {`n" +
        "  tokenizer = tokenizer + `"x`"`n  token_budget = token_budget + 1`n" +
        "  promptTokenCount = promptTokenCount + 1`n  return len(tokenizer) + token_budget + promptTokenCount`n}`n"
    Write-TestText -Path (Join-Path $goOrdinaryNames 'internal/fixture/safe.go') -Text $goOrdinaryNamesText
    Assert-GatePasses -Name 'Go tokenizer and token accounting names remain non-credential data' -Root $goOrdinaryNames

    $goAccumulatorName = Join-Codes @(97, 100, 100, 84, 111, 107, 101, 110)
    $goUsageAccumulatorName = Join-Codes @(97, 100, 100, 85, 115, 97, 103, 101, 84, 111, 107, 101, 110, 67, 111, 117, 110, 116)
    $goCacheRateName = Join-Codes @(84, 111, 107, 101, 110, 87, 101, 105, 103, 104, 116, 101, 100, 67, 97, 99, 104, 101, 72, 105, 116, 82, 97, 116, 101)
    $goDeepSeekKeyName = Join-Codes @(100, 101, 101, 112, 115, 101, 101, 107, 65, 80, 73, 75, 101, 121)
    $goUsageAccounting = New-CleanFixture -Name 'go exact usage accounting metadata'
    $goUsageAccountingText =
        "package fixture`n`nimport `"fmt`"`n`ntype Result struct {`n  InputTokens uint64`n  CachedInputTokens uint64`n  " +
        $goCacheRateName + " string`n}`n`nfunc aggregate(result *Result) {`n  " + $goUsageAccumulatorName +
        " := func(field string, total *uint64, value uint64) {`n    if ^uint64(0)-*total < value { panic(field) }`n" +
        "    *total += value`n  }`n  " + $goUsageAccumulatorName + "(`"input`", &result.InputTokens, 1)`n" +
        "  if result.InputTokens == 0 {`n    result." + $goCacheRateName + " = `"0.000000%`"`n  } else {`n" +
        "    result." + $goCacheRateName + " = fmt.Sprintf(`"%.6f%%`",`n" +
        "      100*float64(result.CachedInputTokens)/float64(result.InputTokens),`n    )`n  }`n}`n"
    Write-TestText -Path (Join-Path $goUsageAccounting 'internal/fixture/safe.go') -Text $goUsageAccountingText
    Assert-GatePasses -Name 'Go explicit usage token-count accumulator and weighted cache-hit percentage are usage metadata' -Root $goUsageAccounting

    $goAccumulatorLiteral = New-CleanFixture -Name 'go token accumulator literal near miss'
    Write-TestText -Path (Join-Path $goAccumulatorLiteral 'internal/fixture/bad.go') -Text (
        "package fixture`n`nvar " + $goAccumulatorName + " = `"hardcoded-value`"`n"
    )
    Assert-GateFails -Name 'Go token accumulator name cannot hold a literal' -Root $goAccumulatorLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $goAccumulatorWrongFunction = New-CleanFixture -Name 'go token accumulator function near miss'
    Write-TestText -Path (Join-Path $goAccumulatorWrongFunction 'internal/fixture/bad.go') -Text (
        "package fixture`n`nvar " + $goAccumulatorName + " = func() string { return `"hardcoded-value`" }`n"
    )
    Assert-GateFails -Name 'Go token accumulator requires the exact numeric function signature' -Root $goAccumulatorWrongFunction -Rule 'PT_SECRET_ASSIGNMENT'

    $goAccumulatorExactSignature = New-CleanFixture -Name 'go token accumulator exact-signature near miss'
    Write-TestText -Path (Join-Path $goAccumulatorExactSignature 'internal/fixture/bad.go') -Text (
        "package fixture`n`nfunc bad() {`n  " + $goAccumulatorName +
        " := func(field string, total *uint64, value uint64) {`n    *total += value`n    _ = field`n  }`n" +
        "  _ = " + $goAccumulatorName + "`n}`n"
    )
    Assert-GateFails -Name 'Go ambiguous token accumulator remains fail-closed even with the numeric signature' -Root $goAccumulatorExactSignature -Rule 'PT_SECRET_ASSIGNMENT'

    $goAccumulatorNestedCredential = New-CleanFixture -Name 'go token accumulator nested credential'
    Write-TestText -Path (Join-Path $goAccumulatorNestedCredential 'internal/fixture/bad.go') -Text (
        "package fixture`n`nfunc bad() {`n  " + $goAccumulatorName +
        " := func(field string, total *uint64, value uint64) {`n    " + $goDeepSeekKeyName +
        " := `"hardcoded-value`"`n    _ = " + $goDeepSeekKeyName + "`n  }`n  _ = " + $goAccumulatorName + "`n}`n"
    )
    Assert-GateFails -Name 'Go exact token accumulator still scans nested credential assignments' -Root $goAccumulatorNestedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goCacheRateLiteral = New-CleanFixture -Name 'go cache rate literal near miss'
    Write-TestText -Path (Join-Path $goCacheRateLiteral 'internal/fixture/bad.go') -Text (
        "package fixture`n`ntype Result struct { " + $goCacheRateName + " string }`n" +
        "func bad(result *Result) { result." + $goCacheRateName + " = `"hardcoded-value`" }`n"
    )
    Assert-GateFails -Name 'Go token-weighted cache-hit rate rejects arbitrary literals' -Root $goCacheRateLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $goCacheRateFormatter = New-CleanFixture -Name 'go cache rate formatter near miss'
    Write-TestText -Path (Join-Path $goCacheRateFormatter 'internal/fixture/bad.go') -Text (
        "package fixture`n`nimport `"fmt`"`n`ntype Result struct { " + $goCacheRateName + " string }`n" +
        "func bad(result *Result, " + $goDeepSeekKeyName + " string) {`n  result." + $goCacheRateName +
        " = fmt.Sprintf(`"%s`", " + $goDeepSeekKeyName + ")`n}`n"
    )
    Assert-GateFails -Name 'Go token-weighted cache-hit rate rejects arbitrary formatting' -Root $goCacheRateFormatter -Rule 'PT_SECRET_ASSIGNMENT'

    $goComplexType = New-CleanFixture -Name 'go complex typed declaration'
    $goComplexTypeText =
        "package fixture`n`nvar " + $fieldWordA + " interface{} = `"hardcoded-value`"`n"
    Write-TestText -Path (Join-Path $goComplexType 'internal/fixture/bad.go') -Text $goComplexTypeText
    Assert-GateFails -Name 'Go complex typed credential declaration cannot hide a literal' -Root $goComplexType -Rule 'PT_SECRET_ASSIGNMENT'

    $camelFieldName = Join-Codes @(86, 101, 114, 105, 102, 105, 99, 97, 116, 105, 111, 110, 84, 111, 107, 101, 110)
    $goCamelBoundary = New-CleanFixture -Name 'go camel credential field'
    $goCamelBoundaryText =
        "package fixture`n`ntype Holder struct { " + $camelFieldName + " string }`n" +
        "var bad = Holder{" + $camelFieldName + ": `"hardcoded-value`"}`n"
    Write-TestText -Path (Join-Path $goCamelBoundary 'internal/fixture/bad.go') -Text $goCamelBoundaryText
    Assert-GateFails -Name 'Go CamelCase credential suffix is detected' -Root $goCamelBoundary -Rule 'PT_SECRET_ASSIGNMENT'

    $goUnknownCall = New-CleanFixture -Name 'go unknown dynamic accessor'
    $goUnknownCallText =
        "package fixture`n`nfunc bad(ctx any) any { " + $fieldWordA +
        " := loadRuntime(ctx); return " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goUnknownCall 'internal/fixture/bad.go') -Text $goUnknownCallText
    Assert-GateFails -Name 'Go unknown function call is not a credential indirection' -Root $goUnknownCall -Rule 'PT_SECRET_ASSIGNMENT'

    $goTrustedAccessor = New-CleanFixture -Name 'go trusted external accessor'
    $goTrustedAccessorText =
        "package fixture`n`nimport `"context`"`n`ntype TokenSource interface { Token(context.Context) (string, error) }`n" +
        "type Account struct {`n  TokenSource TokenSource`n}`ntype Adapter struct {`n  accounts map[string]Account`n}`n" +
        "func (a *Adapter) safe(ctx context.Context, id string) (string, error) {`n" +
        "  account, ok := a.accounts[id]`n  if !ok || account.TokenSource == nil {`n    return `"`", nil`n  }`n  " +
        $fieldWordA + ", err := account.TokenSource.Token(ctx)`n  return " + $fieldWordA + ", err`n}`n"
    Write-TestText -Path (Join-Path $goTrustedAccessor 'internal/fixture/safe.go') -Text $goTrustedAccessorText
    Assert-GateFails -Name 'Go forged external accessor contract cannot authorize a credential call' -Root $goTrustedAccessor -Rule 'PT_SECRET_ASSIGNMENT'

    $goAccessorNearMiss = New-CleanFixture -Name 'go external accessor near miss'
    $goAccessorNearMissText = $goTrustedAccessorText.Replace(
        $fieldWordA + ', err := account.TokenSource.Token(ctx)',
        $fieldWordA + ', err := other.TokenSource.Token(ctx)'
    )
    Write-TestText -Path (Join-Path $goAccessorNearMiss 'internal/fixture/bad.go') -Text $goAccessorNearMissText
    Assert-GateFails -Name 'Go external credential accessor near miss fails closed' -Root $goAccessorNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $goCaseLabels = New-CleanFixture -Name 'go case labels'
    $goCaseLabelsText =
        "package fixture`n`nfunc safe(kind string) { switch kind {`ncase `"bearer`", `"basic`", `"" +
        $fieldWordA + "`", `"apikey`", `"api-key`":`n}`n}`n"
    Write-TestText -Path (Join-Path $goCaseLabels 'internal/fixture/safe.go') -Text $goCaseLabelsText
    Assert-GatePasses -Name 'Go case labels are not assignments' -Root $goCaseLabels

    $referenceContainerName = Join-Codes @(115, 101, 99, 114, 101, 116, 95, 114, 101, 102, 115)
    $goTypedReferenceMap = New-CleanFixture -Name 'go exact reference metadata'
    $goTypedReferenceMapText =
        "package fixture`n`nfunc safe(document map[string]any) {`n  document[`"" + $referenceContainerName +
        "`"] = []any{map[string]any{`n    `"provider`": `"vault.local`",`n    `"scope`": `"WORKSPACE`",`n" +
        "    `"scope_id`": `"ops`",`n    `"name`": `"model-key`",`n    `"version`": `"1`",`n  }}`n}`n"
    Write-TestText -Path (Join-Path $goTypedReferenceMap 'internal/fixture/safe.go') -Text $goTypedReferenceMapText
    Assert-GatePasses -Name 'Go exact structured reference metadata is not credential material' -Root $goTypedReferenceMap

    $goReferenceMapNearMiss = New-CleanFixture -Name 'go reference metadata near miss'
    $goReferenceMapNearMissText = $goTypedReferenceMapText.Replace(
        "    `"version`": `"1`",",
        "    `"version`": `"1`",`n    `"" + $fieldWordA + "`": `"hardcoded-value`","
    )
    Write-TestText -Path (Join-Path $goReferenceMapNearMiss 'internal/fixture/bad.go') -Text $goReferenceMapNearMissText
    Assert-GateFails -Name 'Go reference metadata with an extra credential field fails closed' -Root $goReferenceMapNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $goReferenceMapMissing = New-CleanFixture -Name 'go reference metadata missing field'
    $goReferenceMapMissingText = $goTypedReferenceMapText.Replace("    `"version`": `"1`",`n", '')
    Write-TestText -Path (Join-Path $goReferenceMapMissing 'internal/fixture/bad.go') -Text $goReferenceMapMissingText
    Assert-GateFails -Name 'Go reference metadata missing an exact field fails closed' -Root $goReferenceMapMissing -Rule 'PT_SECRET_ASSIGNMENT'

    $goReferenceMapDuplicate = New-CleanFixture -Name 'go reference metadata duplicate field'
    $goReferenceMapDuplicateText = $goTypedReferenceMapText.Replace(
        "    `"version`": `"1`",",
        "    `"version`": `"1`",`n    `"version`": `"2`","
    )
    Write-TestText -Path (Join-Path $goReferenceMapDuplicate 'internal/fixture/bad.go') -Text $goReferenceMapDuplicateText
    Assert-GateFails -Name 'Go reference metadata duplicate exact field fails closed' -Root $goReferenceMapDuplicate -Rule 'PT_SECRET_ASSIGNMENT'

    $goReferenceMapExtra = New-CleanFixture -Name 'go reference metadata extra field'
    $goReferenceMapExtraText = $goTypedReferenceMapText.Replace(
        "    `"version`": `"1`",",
        "    `"version`": `"1`",`n    `"description`": `"public`","
    )
    Write-TestText -Path (Join-Path $goReferenceMapExtra 'internal/fixture/bad.go') -Text $goReferenceMapExtraText
    Assert-GateFails -Name 'Go reference metadata extra non-credential field fails closed' -Root $goReferenceMapExtra -Rule 'PT_SECRET_ASSIGNMENT'

    $goMultilineCall = New-CleanFixture -Name 'go multiline dynamic call'
    $goMultilineCallText =
        "package fixture`n`nfunc bad(ctx any) any {`n  " + $fieldWordA + " := combine(`n    ctx,`n" +
        "    `"hardcoded-tail`",`n  )`n  return " + $fieldWordA + "`n}`n"
    Write-TestText -Path (Join-Path $goMultilineCall 'internal/fixture/bad.go') -Text $goMultilineCallText
    Assert-GateFails -Name 'Go multiline dynamic call is evaluated as one credential expression' -Root $goMultilineCall -Rule 'PT_SECRET_ASSIGNMENT'

    $goShadowedOSCredential = New-CleanFixture -Name 'go shadowed os credential'
    $goShadowedOSCredentialText =
        "package fixture`n`ntype fakeOS struct{}`nfunc (fakeOS) Getenv(v string) string { return v }`n" +
        "var os fakeOS`nvar " + $fieldWordA + " = os.Getenv(`"hardcoded-value`")`n"
    Write-TestText -Path (Join-Path $goShadowedOSCredential 'internal/fixture/bad.go') -Text $goShadowedOSCredentialText
    Assert-GateFails -Name 'Go shadowed os identifier cannot impersonate stdlib env access' -Root $goShadowedOSCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goCanonicalOSCredential = New-CleanFixture -Name 'go canonical os credential target'
    $goCanonicalOSCredentialText =
        "package fixture`n`nimport `"os`"`nfunc bad() string { " + $fieldWordA +
        " := os.Getenv(`"MODEL_TOKEN`"); return " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goCanonicalOSCredential 'internal/fixture/bad.go') -Text $goCanonicalOSCredentialText
    Assert-GateFails -Name 'Go stdlib env call has no sensitive-target special allowance' -Root $goCanonicalOSCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goShadowedNewCredential = New-CleanFixture -Name 'go shadowed new credential'
    $goShadowedNewCredentialText =
        "package fixture`n`nfunc new(v any) string { return `"hardcoded-value`" }`nfunc bad(runtimeValue any) string { " +
        $fieldWordA + " := new(runtimeValue); return " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goShadowedNewCredential 'internal/fixture/bad.go') -Text $goShadowedNewCredentialText
    Assert-GateFails -Name 'Go shadowed predeclared new cannot hide a hardcoded credential' -Root $goShadowedNewCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goShadowedMakeCredential = New-CleanFixture -Name 'go shadowed make credential'
    $goShadowedMakeCredentialText =
        "package fixture`n`nfunc make(v any) string { return `"hardcoded-value`" }`nfunc bad(runtimeValue any) string { " +
        $fieldWordA + " := make(runtimeValue); return " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goShadowedMakeCredential 'internal/fixture/bad.go') -Text $goShadowedMakeCredentialText
    Assert-GateFails -Name 'Go shadowed predeclared make cannot hide a hardcoded credential' -Root $goShadowedMakeCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goGroupedTypedVariable = New-CleanFixture -Name 'go grouped typed variable declaration'
    $goGroupedTypedVariableText =
        "package fixture`n`nvar (`n  " + $fieldWordA + " interface{} = `"hardcoded-value`"`n)`n"
    Write-TestText -Path (Join-Path $goGroupedTypedVariable 'internal/fixture/bad.go') -Text $goGroupedTypedVariableText
    Assert-GateFails -Name 'Go grouped complex typed variable declaration cannot hide a literal' -Root $goGroupedTypedVariable -Rule 'PT_SECRET_ASSIGNMENT'

    $goGroupedTypedConstant = New-CleanFixture -Name 'go grouped typed constant declaration'
    $goGroupedTypedConstantText =
        "package fixture`n`nconst (`n  " + $fieldWordA + " string = `"hardcoded-value`"`n)`n"
    Write-TestText -Path (Join-Path $goGroupedTypedConstant 'internal/fixture/bad.go') -Text $goGroupedTypedConstantText
    Assert-GateFails -Name 'Go grouped typed constant declaration cannot hide a literal' -Root $goGroupedTypedConstant -Rule 'PT_SECRET_ASSIGNMENT'

    $goGroupedInlineVariable = New-CleanFixture -Name 'go grouped same line variable declarations'
    $goGroupedInlineVariableText =
        "package fixture`n`nvar ( ordinary int = 0; " + $fieldWordA +
        " interface{} = `"hardcoded-value`" )`n"
    Write-TestText -Path (Join-Path $goGroupedInlineVariable 'internal/fixture/bad.go') -Text $goGroupedInlineVariableText
    Assert-GateFails -Name 'Go grouped same-line second variable declaration cannot hide a literal' -Root $goGroupedInlineVariable -Rule 'PT_SECRET_ASSIGNMENT'

    $goGroupedInlineConstant = New-CleanFixture -Name 'go grouped same line constant declarations'
    $goGroupedInlineConstantText =
        "package fixture`n`nconst ( ordinary int = 0; " + $fieldWordA +
        " string = `"hardcoded-value`" )`n"
    Write-TestText -Path (Join-Path $goGroupedInlineConstant 'internal/fixture/bad.go') -Text $goGroupedInlineConstantText
    Assert-GateFails -Name 'Go grouped same-line second constant declaration cannot hide a literal' -Root $goGroupedInlineConstant -Rule 'PT_SECRET_ASSIGNMENT'

    $goHardcodedCredential = New-CleanFixture -Name 'go hardcoded credential'
    $goHardcodedText = "package fixture`n`ntype Holder struct { Token string }`nvar bad = Holder{" +
        (Join-Codes @(84, 111, 107, 101, 110)) + ": `"hardcoded-value`"}`n"
    Write-TestText -Path (Join-Path $goHardcodedCredential 'internal/fixture/bad.go') -Text $goHardcodedText
    Assert-GateFails -Name 'Go direct hardcoded credential literal' -Root $goHardcodedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goNearBypassCredential = New-CleanFixture -Name 'go credential near bypass'
    $goNearBypassText = "package fixture`n`nvar " + $fieldWordA + " = `"hard`" + suffix`n"
    Write-TestText -Path (Join-Path $goNearBypassCredential 'internal/fixture/bad.go') -Text $goNearBypassText
    Assert-GateFails -Name 'Go concatenated credential near bypass fails closed' -Root $goNearBypassCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goDynamicCallLiteral = New-CleanFixture -Name 'go dynamic call literal credential'
    $goDynamicCallLiteralText = "package fixture`n`nvar " + (Join-Codes @(115, 101, 99, 114, 101, 116)) +
        " = combine(runtimeValue, `"hardcoded-tail`")`n"
    Write-TestText -Path (Join-Path $goDynamicCallLiteral 'internal/fixture/bad.go') -Text $goDynamicCallLiteralText
    Assert-GateFails -Name 'Go call with dynamic input and hardcoded credential tail fails closed' -Root $goDynamicCallLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $constructorName = Join-Codes @(83, 101, 99, 114, 101, 116, 82, 101, 102)
    $goUnknownConstructor = New-CleanFixture -Name 'go unknown credential constructor'
    $goUnknownConstructorText =
        "package fixture`n`nvar " + $fieldWordA + " = " + $constructorName + "(`"hardcoded-value`")`n"
    Write-TestText -Path (Join-Path $goUnknownConstructor 'internal/fixture/bad.go') -Text $goUnknownConstructorText
    Assert-GateFails -Name 'Go unknown credential constructor with a literal fails closed' -Root $goUnknownConstructor -Rule 'PT_SECRET_ASSIGNMENT'

    $goNestedCredentialContainer = New-CleanFixture -Name 'go nested credential container'
    $goNestedCredentialContainerText =
        "package fixture`n`ntype Holder struct { Token any }`nvar bad = Holder{" +
        (Join-Codes @(84, 111, 107, 101, 110)) +
        ": map[string]any{Kind: runtimeValue, `"value`": `"hardcoded-nested`"}}`n"
    Write-TestText -Path (Join-Path $goNestedCredentialContainer 'internal/fixture/bad.go') -Text $goNestedCredentialContainerText
    Assert-GateFails -Name 'Go sensitive container with nested hardcoded value fails closed' -Root $goNestedCredentialContainer -Rule 'PT_SECRET_ASSIGNMENT'

    $goReferenceContainer = New-CleanFixture -Name 'go reference container raw value'
    $goReferenceContainerText =
        "package fixture`n`ntype Holder struct { TokenRefs []string }`nvar bad = Holder{" +
        (Join-Codes @(84, 111, 107, 101, 110, 82, 101, 102, 115)) +
        ": []string{`"hardcoded-reference-value`"}}`n"
    Write-TestText -Path (Join-Path $goReferenceContainer 'internal/fixture/bad.go') -Text $goReferenceContainerText
    Assert-GateFails -Name 'Go reference container cannot hide a raw literal' -Root $goReferenceContainer -Rule 'PT_SECRET_ASSIGNMENT'

    $goReplaceAllCredential = New-CleanFixture -Name 'go replace all hardcoded tail'
    $goReplaceAllCredentialText =
        "package fixture`n`nfunc bad(dynamic string) string { " + $fieldWordA +
        " := strings.ReplaceAll(dynamic, `"x`", `"hardcoded-replacement`"); return " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goReplaceAllCredential 'internal/fixture/bad.go') -Text $goReplaceAllCredentialText
    Assert-GateFails -Name 'Go ReplaceAll cannot hide a hardcoded credential replacement' -Root $goReplaceAllCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goMultilineFieldCredential = New-CleanFixture -Name 'go multiline field credential'
    $goMultilineFieldCredentialText =
        "package fixture`n`ntype Holder struct { Token string }`nvar bad = Holder{`n " +
        (Join-Codes @(84, 111, 107, 101, 110)) + ":`n  `"hardcoded-multiline`",`n}`n"
    Write-TestText -Path (Join-Path $goMultilineFieldCredential 'internal/fixture/bad.go') -Text $goMultilineFieldCredentialText
    Assert-GateFails -Name 'Go multiline sensitive field fails closed at an unknown RHS' -Root $goMultilineFieldCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goMultilineRawCredential = New-CleanFixture -Name 'go multiline raw credential'
    $goMultilineRawCredentialText =
        "package fixture`n`nvar payload = " + [char]96 + "`n// {`"" + $fieldWordB +
        "`":`"hardcoded-in-raw`"}`n" + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goMultilineRawCredential 'internal/fixture/bad.go') -Text $goMultilineRawCredentialText
    Assert-GateFails -Name 'Go multiline raw string keeps slash text visible to credential scan' -Root $goMultilineRawCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goRawGroupedCredential = New-CleanFixture -Name 'go raw grouped credential declaration'
    $goRawGroupedCredentialText =
        "package fixture`n`nvar payload = " + [char]96 + "`nvar (`n  " + $fieldWordA +
        " interface{} = `"hardcoded-in-raw`"`n)`n" + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goRawGroupedCredential 'internal/fixture/bad.go') -Text $goRawGroupedCredentialText
    Assert-GateFails -Name 'Go raw content grouped typed credential declaration fails closed' -Root $goRawGroupedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goRawInlineGroupedCredential = New-CleanFixture -Name 'go raw inline grouped credential declaration'
    $goRawInlineGroupedCredentialText =
        "package fixture`n`nvar payload = " + [char]96 + "var ( ordinary int = 0; " +
        $fieldWordA + " interface{} = `"hardcoded-in-raw`" )" + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goRawInlineGroupedCredential 'internal/fixture/bad.go') -Text $goRawInlineGroupedCredentialText
    Assert-GateFails -Name 'Go raw content same-line second grouped declaration fails closed' -Root $goRawInlineGroupedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goRawCommentPrefixedCredential = New-CleanFixture -Name 'go raw comment prefixed credential declaration'
    $goRawCommentPrefixedCredentialText =
        "package fixture`n`nvar payload = " + [char]96 + "`n// " + $fieldWordA +
        " interface{} = `"hardcoded-in-raw`"`n" + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goRawCommentPrefixedCredential 'internal/fixture/bad.go') -Text $goRawCommentPrefixedCredentialText
    Assert-GateFails -Name 'Go raw content comment-looking typed credential remains visible' -Root $goRawCommentPrefixedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goRawDynamicCredential = New-CleanFixture -Name 'go raw dynamic credential declaration'
    $goRawDynamicCredentialText =
        "package fixture`n`nvar payload = " + [char]96 + "`nvar (`n  " + $fieldWordA +
        " interface{} = runtimeValue`n)`n" + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goRawDynamicCredential 'internal/fixture/safe.go') -Text $goRawDynamicCredentialText
    Assert-GatePasses -Name 'Go raw typed credential text with a dynamic value is not blanket rejected' -Root $goRawDynamicCredential

    $goCommentGroupedCredential = New-CleanFixture -Name 'go comment grouped credential declaration'
    $goCommentGroupedCredentialText =
        "package fixture`n`n// var (`n//   " + $fieldWordA +
        " interface{} = `"comment-only`"`n//)`n"
    Write-TestText -Path (Join-Path $goCommentGroupedCredential 'internal/fixture/safe.go') -Text $goCommentGroupedCredentialText
    Assert-GatePasses -Name 'Go source comments remain excluded from credential assignments' -Root $goCommentGroupedCredential

    $goRawCloseCredential = New-CleanFixture -Name 'go raw close credential'
    $goRawCloseCredentialText =
        "package fixture`n`nfunc bad() { _ = " + [char]96 + "prefix`n/*`n" + [char]96 +
        "; " + $fieldWordA + " := `"hardcoded-after-raw`"; _ = " + $fieldWordA + " }`n"
    Write-TestText -Path (Join-Path $goRawCloseCredential 'internal/fixture/bad.go') -Text $goRawCloseCredentialText
    Assert-GateFails -Name 'Go raw-string close cannot turn following code into a comment' -Root $goRawCloseCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $goDamagedCredential = New-CleanFixture -Name 'go damaged credential syntax'
    $goDamagedCredentialText = "package fixture`n`nvar " + $fieldWordA + " = `"unterminated`n"
    Write-TestText -Path (Join-Path $goDamagedCredential 'internal/fixture/bad.go') -Text $goDamagedCredentialText
    Assert-GateFails -Name 'damaged Go credential literal fails closed' -Root $goDamagedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $priceUnitClassName = Join-Codes @(84, 111, 107, 101, 110, 67, 108, 97, 115, 115)
    $decisionResultName = Join-Codes @(65, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 82, 101, 115, 117, 108, 116)
    $maximumEstimateName = Join-Codes @(77, 97, 120, 83, 107, 105, 108, 108, 77, 68, 84, 111, 107, 101, 110, 69, 115, 116, 105, 109, 97, 116, 101)
    $maximumValueBytesName = Join-Codes @(77, 97, 120, 83, 101, 99, 114, 101, 116, 86, 97, 108, 117, 101, 66, 121, 116, 101, 115)
    $invalidDiagnosticName = Join-Codes @(69, 114, 114, 83, 101, 99, 114, 101, 116, 86, 97, 108, 117, 101, 73, 110, 118, 97, 108, 105, 100)
    $destroyedDiagnosticName = Join-Codes @(69, 114, 114, 83, 101, 99, 114, 101, 116, 86, 97, 108, 117, 101, 68, 101, 115, 116, 114, 111, 121, 101, 100)
    $serializationDiagnosticName = Join-Codes @(69, 114, 114, 83, 101, 99, 114, 101, 116, 86, 97, 108, 117, 101, 83, 101, 114, 105, 97, 108, 105, 122, 97, 116, 105, 111, 110)
    $scopeMismatchDiagnosticName = Join-Codes @(69, 114, 114, 83, 101, 99, 114, 101, 116, 83, 99, 111, 112, 101, 77, 105, 115, 109, 97, 116, 99, 104)
    $scopeTypeName = Join-Codes @(83, 101, 99, 114, 101, 116, 83, 99, 111, 112, 101)
    $tenantScopeName = Join-Codes @(83, 101, 99, 114, 101, 116, 84, 101, 110, 97, 110, 116)
    $workspaceScopeName = Join-Codes @(83, 101, 99, 114, 101, 116, 87, 111, 114, 107, 115, 112, 97, 99, 101)
    $agentScopeName = Join-Codes @(83, 101, 99, 114, 101, 116, 65, 103, 101, 110, 116)
    $installationScopeName = Join-Codes @(83, 101, 99, 114, 101, 116, 77, 111, 100, 117, 108, 101, 73, 110, 115, 116, 97, 108, 108, 97, 116, 105, 111, 110)
    $sourceBytesName = Join-Codes @(115, 101, 99, 114, 101, 116, 66, 121, 116, 101, 115)
    $revisionFieldName = Join-Codes @(65, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 82, 101, 118, 105, 115, 105, 111, 110)
    $revisionWireFieldName = Join-Codes @(97, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 95, 114, 101, 118, 105, 115, 105, 111, 110)
    $csrfBytesV1Name = Join-Codes @(67, 83, 82, 70, 84, 111, 107, 101, 110, 66, 121, 116, 101, 115, 86, 49)

    $semanticMetadataDocs = New-CleanFixture -Name 'exact semantic metadata docs'
    $semanticMetadataDocsText =
        $priceUnitClassName + " = INPUT | CACHE_HIT_INPUT | CACHE_MISS_INPUT | OUTPUT | REASONING`n" +
        $decisionResultName + "=VALID,ModelUnknownOperatorDecisionObservationHash`n" +
        $decisionResultName + "=INVALID`n"
    Write-TestText -Path (Join-Path $semanticMetadataDocs 'docs/semantic-metadata.md') -Text $semanticMetadataDocsText
    Assert-GatePasses -Name 'exact semantic metadata grammars remain public-safe' -Root $semanticMetadataDocs

    $semanticClassNearMiss = New-CleanFixture -Name 'semantic class near miss'
    Write-TestText -Path (Join-Path $semanticClassNearMiss 'docs/bad.md') -Text (
        $priceUnitClassName + " = INPUT | CACHE_HIT_INPUT | CACHE_MISS_INPUT | OUTPUT | REASONING | BILLING`n"
    )
    Assert-GateFails -Name 'semantic class grammar rejects added values' -Root $semanticClassNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $semanticClassLowercase = New-CleanFixture -Name 'semantic class lowercase values'
    Write-TestText -Path (Join-Path $semanticClassLowercase 'docs/bad.md') -Text (
        $priceUnitClassName + " = input | cache_hit_input | cache_miss_input | output | reasoning`n"
    )
    Assert-GateFails -Name 'semantic class grammar rejects lowercase values' -Root $semanticClassLowercase -Rule 'PT_SECRET_ASSIGNMENT'

    $semanticClassMixedCase = New-CleanFixture -Name 'semantic class mixed case values'
    Write-TestText -Path (Join-Path $semanticClassMixedCase 'docs/bad.md') -Text (
        $priceUnitClassName + " = INPUT | Cache_Hit_Input | CACHE_MISS_INPUT | OUTPUT | REASONING`n"
    )
    Assert-GateFails -Name 'semantic class grammar rejects mixed case values' -Root $semanticClassMixedCase -Rule 'PT_SECRET_ASSIGNMENT'

    $decisionResultNearMiss = New-CleanFixture -Name 'decision result near miss'
    Write-TestText -Path (Join-Path $decisionResultNearMiss 'docs/bad.md') -Text (
        $decisionResultName + "=VALID,AttackerControlledObservationHash`n"
    )
    Assert-GateFails -Name 'decision result grammar rejects unknown trailing metadata' -Root $decisionResultNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $decisionResultLowercase = New-CleanFixture -Name 'decision result lowercase value'
    Write-TestText -Path (Join-Path $decisionResultLowercase 'docs/bad.md') -Text (
        $decisionResultName + "=valid`n"
    )
    Assert-GateFails -Name 'decision result grammar rejects lowercase values' -Root $decisionResultLowercase -Rule 'PT_SECRET_ASSIGNMENT'

    $decisionResultMixedCase = New-CleanFixture -Name 'decision result mixed case value'
    Write-TestText -Path (Join-Path $decisionResultMixedCase 'docs/bad.md') -Text (
        $decisionResultName + "=VaLiD`n"
    )
    Assert-GateFails -Name 'decision result grammar rejects mixed case values' -Root $decisionResultMixedCase -Rule 'PT_SECRET_ASSIGNMENT'

    $goSemanticMetadata = New-CleanFixture -Name 'go exact semantic metadata'
    $goSemanticMetadataText =
        "package fixture`n`nimport `"errors`"`n`n" +
        "const " + $maximumEstimateName + " = 16 << 10`n" +
        "const " + $maximumValueBytesName + " = 1048576`n" +
        "var (`n" +
        "  " + $invalidDiagnosticName + " = errors.New(`"moduleapi: invalid SecretValue`")`n" +
        "  " + $destroyedDiagnosticName + " = errors.New(`"moduleapi: SecretValue is destroyed`")`n" +
        "  " + $serializationDiagnosticName + " = errors.New(`"moduleapi: SecretValue serialization is forbidden`")`n" +
        "  " + $scopeMismatchDiagnosticName + " = errors.New(`"moduleapi: secret scope mismatch`")`n" +
        ")`n`ntype " + $scopeTypeName + " string`n`nconst (`n" +
        "  " + $tenantScopeName + " " + $scopeTypeName + " = `"TENANT`"`n" +
        "  " + $workspaceScopeName + " " + $scopeTypeName + " = `"WORKSPACE`"`n" +
        "  " + $agentScopeName + " " + $scopeTypeName + " = `"AGENT`"`n" +
        "  " + $installationScopeName + " " + $scopeTypeName + " = `"MODULE_INSTALLATION`"`n" +
        ")`n"
    Write-TestText -Path (Join-Path $goSemanticMetadata 'internal/fixture/metadata.go') -Text $goSemanticMetadataText
    Assert-GatePasses -Name 'Go metadata requires exact names values types and declaration kinds' -Root $goSemanticMetadata

    $goNumericShapeMetadata = New-CleanFixture -Name 'go numeric credential-shaped metadata'
    $goNumericShapeMetadataText =
        "package fixture`n`ntype snapshot struct { " + $revisionFieldName + " uint64 }`n" +
        "const " + $csrfBytesV1Name + " = 32`n" +
        "var current = snapshot{" + $revisionFieldName + ": 3}`n" +
        "var wire = " + [char]96 + '{"' + $revisionWireFieldName + '":3}' + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goNumericShapeMetadata 'internal/fixture/metadata.go') -Text $goNumericShapeMetadataText
    Assert-GatePasses -Name 'Go revision and byte-size fields permit exact decimal shape metadata' -Root $goNumericShapeMetadata

    $goNumericShapeString = New-CleanFixture -Name 'go credential-shaped metadata string near miss'
    $goNumericShapeStringText =
        "package fixture`n`nconst " + $csrfBytesV1Name + " = `"hardcoded-value`"`n" +
        "var wire = " + [char]96 + '{"' + $revisionWireFieldName + '":"hardcoded-value"}' + [char]96 + "`n"
    Write-TestText -Path (Join-Path $goNumericShapeString 'internal/fixture/bad.go') -Text $goNumericShapeStringText
    Assert-GateFails -Name 'Go credential-shaped metadata rejects string material' -Root $goNumericShapeString -Rule 'PT_SECRET_ASSIGNMENT'

    $goNumericShapeNearName = New-CleanFixture -Name 'go credential-shaped metadata suffix near miss'
    Write-TestText -Path (Join-Path $goNumericShapeNearName 'internal/fixture/bad.go') -Text (
        "package fixture`n`nconst " + $csrfBytesV1Name + "Backup = 32`n"
    )
    Assert-GateFails -Name 'Go credential-shaped metadata requires an exact shape suffix' -Root $goNumericShapeNearName -Rule 'PT_SECRET_ASSIGNMENT'

    $exactReferenceHashName = Join-Codes @(69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 86, 101, 114, 115, 105, 111, 110, 83, 101, 116, 72, 97, 115, 104)
    $finalSanitizedRequestName = Join-Codes @(70, 105, 110, 97, 108, 83, 101, 99, 114, 101, 116, 70, 114, 101, 101, 69, 110, 99, 111, 100, 101, 100, 82, 101, 113, 117, 101, 115, 116)
    $publicationEvidenceHashName = Join-Codes @(80, 117, 98, 108, 105, 99, 97, 116, 105, 111, 110, 65, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 69, 118, 105, 100, 101, 110, 99, 101, 72, 97, 115, 104)
    $maximumExactReferencesName = Join-Codes @(77, 97, 120, 69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 115)
    $typedReferenceSetName = Join-Codes @(83, 101, 99, 114, 101, 116, 82, 101, 102, 115)
    $referenceSetLocalName = Join-Codes @(115, 101, 99, 114, 101, 116, 83, 101, 116)
    $exactBindingSetName = Join-Codes @(69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 66, 105, 110, 100, 105, 110, 103, 115)
    $exactReferenceInputName = Join-Codes @(69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 115)
    $referenceSetFactoryName = Join-Codes @(78, 101, 119, 69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 86, 101, 114, 115, 105, 111, 110, 83, 101, 116, 86, 49)
    $referenceSetFromWireName = Join-Codes @(101, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 86, 101, 114, 115, 105, 111, 110, 83, 101, 116, 70, 114, 111, 109, 87, 105, 114, 101)
    $referenceSetCloneName = Join-Codes @(99, 108, 111, 110, 101, 69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 86, 101, 114, 115, 105, 111, 110, 83, 101, 116, 87, 105, 114, 101)
    $startupBindingSetFactoryName = Join-Codes @(110, 101, 119, 83, 116, 97, 114, 116, 117, 112, 69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 66, 105, 110, 100, 105, 110, 103, 83, 101, 116, 86, 49)
    $bindingSetLocalName = Join-Codes @(115, 101, 99, 114, 101, 116, 66, 105, 110, 100, 105, 110, 103, 115)
    $exactReferenceSetFieldName = Join-Codes @(69, 120, 97, 99, 116, 83, 101, 99, 114, 101, 116, 82, 101, 102, 86, 101, 114, 115, 105, 111, 110, 83, 101, 116)
    $goProtocolMetadata = New-CleanFixture -Name 'go protocol non-credential metadata'
    $goProtocolMetadataText =
        "package fixture`n`nconst " + $maximumExactReferencesName + " = 16`n" +
        "var " + $finalSanitizedRequestName + " = []byte(`"public request`")`n" +
        "var " + $exactReferenceHashName + " = `"invalid-fixture-hash`"`n" +
        "var " + $publicationEvidenceHashName + " = `"invalid-fixture-hash`"`n" +
        "var " + $typedReferenceSetName + " = []moduleapi.SecretRef{}`n" +
        "func safe(input Input, value Holder) {`n" +
        "  " + $referenceSetLocalName + ", err := " + $referenceSetFactoryName + "(`n    input." + $exactReferenceInputName + ",`n  )`n" +
        "  " + $referenceSetLocalName + ", err = " + $referenceSetFromWireName + "(`n    value." + $exactReferenceSetFieldName + ",`n  )`n" +
        "  value." + $exactReferenceSetFieldName + " =`n    " + $referenceSetCloneName + "(value." + $exactReferenceSetFieldName + ")`n" +
        "  " + $bindingSetLocalName + ", err := " + $startupBindingSetFactoryName + "(`n    value." + $exactBindingSetName + ",`n  )`n" +
        "  value." + $exactBindingSetName + " =`n    value." + $exactBindingSetName + "[:1]`n" +
        "  _, _, _ = " + $referenceSetLocalName + ", " + $bindingSetLocalName + ", err`n}`n"
    Write-TestText -Path (Join-Path $goProtocolMetadata 'internal/fixture/metadata.go') -Text $goProtocolMetadataText
    Assert-GatePasses -Name 'Go protocol reference digest and sanitized payload metadata remain public-safe' -Root $goProtocolMetadata

    $goReferenceLiteralNearMiss = New-CleanFixture -Name 'go direct reference literal near miss'
    Write-TestText -Path (Join-Path $goReferenceLiteralNearMiss 'internal/fixture/bad.go') -Text (
        "package fixture`n`nvar " + $typedReferenceSetName + " = `"hardcoded-value`"`n"
    )
    Assert-GateFails -Name 'Go reference container rejects a direct string literal' -Root $goReferenceLiteralNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $goBindingConcatenationNearMiss = New-CleanFixture -Name 'go binding concatenation near miss'
    Write-TestText -Path (Join-Path $goBindingConcatenationNearMiss 'internal/fixture/bad.go') -Text (
        "package fixture`n`nfunc bad(value Holder) { value." + $exactBindingSetName +
        " = `"hardcoded`" + `"-value`" }`n"
    )
    Assert-GateFails -Name 'Go binding container rejects concatenated string literals' -Root $goBindingConcatenationNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $goReferenceTupleNearMiss = New-CleanFixture -Name 'go reference tuple near miss'
    Write-TestText -Path (Join-Path $goReferenceTupleNearMiss 'internal/fixture/bad.go') -Text (
        "package fixture`n`nfunc bad(ctx any) { " + $referenceSetLocalName + ", err := loadRuntime(ctx); _, _ = " +
        $referenceSetLocalName + ", err }`n"
    )
    Assert-GateFails -Name 'Go reference tuple rejects an unknown accessor' -Root $goReferenceTupleNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $sqlCredentialColumnName = Join-Codes @(115, 101, 99, 114, 101, 116, 95, 110, 97, 109, 101)
    $sqlDynamicReference = New-CleanFixture -Name 'sql dynamic credential column reference'
    Write-TestText -Path (Join-Path $sqlDynamicReference 'internal/currentstore/migrations/fixture.sql') -Text (
        "UPDATE fixture SET " + $sqlCredentialColumnName + "=NEW." + $sqlCredentialColumnName + " AND`nactive=1;`n"
    )
    Assert-GatePasses -Name 'SQL credential columns permit dynamic NEW and OLD references' -Root $sqlDynamicReference

    $sqlLiteralNearMiss = New-CleanFixture -Name 'sql credential literal near miss'
    Write-TestText -Path (Join-Path $sqlLiteralNearMiss 'internal/currentstore/migrations/fixture.sql') -Text (
        "UPDATE fixture SET " + $sqlCredentialColumnName + "='hardcoded-value';`n"
    )
    Assert-GateFails -Name 'SQL credential columns reject direct string literals' -Root $sqlLiteralNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $maximumBytesLiteral = New-CleanFixture -Name 'maximum value bytes literal'
    Write-TestText -Path (Join-Path $maximumBytesLiteral 'internal/fixture/bad.go') -Text (
        "package fixture`n`nconst " + $maximumValueBytesName + " = `"hardcoded-value`"`n"
    )
    Assert-GateFails -Name 'maximum value bytes rejects string material' -Root $maximumBytesLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $maximumEstimateLiteral = New-CleanFixture -Name 'maximum estimate literal'
    Write-TestText -Path (Join-Path $maximumEstimateLiteral 'internal/fixture/bad.go') -Text (
        "package fixture`n`nconst " + $maximumEstimateName + " = `"hardcoded-value`"`n"
    )
    Assert-GateFails -Name 'maximum estimate rejects string material' -Root $maximumEstimateLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $maximumEstimateNearName = New-CleanFixture -Name 'maximum estimate near name'
    Write-TestText -Path (Join-Path $maximumEstimateNearName 'internal/fixture/bad.go') -Text (
        "package fixture`n`nconst " + $maximumEstimateName + "Backup = 16 << 10`n"
    )
    Assert-GateFails -Name 'maximum estimate near name is not allowlisted' -Root $maximumEstimateNearName -Rule 'PT_SECRET_ASSIGNMENT'

    $diagnosticWrongMessage = New-CleanFixture -Name 'diagnostic wrong message'
    Write-TestText -Path (Join-Path $diagnosticWrongMessage 'internal/fixture/bad.go') -Text (
        "package fixture`n`nimport `"errors`"`nvar " + $invalidDiagnosticName + " = errors.New(`"wrong diagnostic`")`n"
    )
    Assert-GateFails -Name 'credential diagnostic requires its exact static message' -Root $diagnosticWrongMessage -Rule 'PT_SECRET_ASSIGNMENT'

    $diagnosticWrongConstructor = New-CleanFixture -Name 'diagnostic wrong constructor'
    Write-TestText -Path (Join-Path $diagnosticWrongConstructor 'internal/fixture/bad.go') -Text (
        "package fixture`n`nimport `"fmt`"`nvar " + $invalidDiagnosticName + " = fmt.Errorf(`"moduleapi: invalid SecretValue`")`n"
    )
    Assert-GateFails -Name 'credential diagnostic requires errors New' -Root $diagnosticWrongConstructor -Rule 'PT_SECRET_ASSIGNMENT'

    $diagnosticNearName = New-CleanFixture -Name 'diagnostic near name'
    Write-TestText -Path (Join-Path $diagnosticNearName 'internal/fixture/bad.go') -Text (
        "package fixture`n`nimport `"errors`"`nvar " + $invalidDiagnosticName + "Backup = errors.New(`"moduleapi: invalid SecretValue`")`n"
    )
    Assert-GateFails -Name 'credential diagnostic near name is not allowlisted' -Root $diagnosticNearName -Rule 'PT_SECRET_ASSIGNMENT'

    $scopeWrongType = New-CleanFixture -Name 'scope wrong type'
    Write-TestText -Path (Join-Path $scopeWrongType 'internal/fixture/bad.go') -Text (
        "package fixture`n`nconst (`n  " + $tenantScopeName + " string = `"TENANT`"`n)`n"
    )
    Assert-GateFails -Name 'scope enum requires its explicit declared type' -Root $scopeWrongType -Rule 'PT_SECRET_ASSIGNMENT'

    $scopeWrongValue = New-CleanFixture -Name 'scope wrong value'
    Write-TestText -Path (Join-Path $scopeWrongValue 'internal/fixture/bad.go') -Text (
        "package fixture`n`ntype " + $scopeTypeName + " string`nconst (`n  " +
        $tenantScopeName + " " + $scopeTypeName + " = `"WORKSPACE`"`n)`n"
    )
    Assert-GateFails -Name 'scope enum name requires its exact value' -Root $scopeWrongValue -Rule 'PT_SECRET_ASSIGNMENT'

    $scopeWrongDeclaration = New-CleanFixture -Name 'scope wrong declaration kind'
    Write-TestText -Path (Join-Path $scopeWrongDeclaration 'internal/fixture/bad.go') -Text (
        "package fixture`n`ntype " + $scopeTypeName + " string`nvar (`n  " +
        $tenantScopeName + " " + $scopeTypeName + " = `"TENANT`"`n)`n"
    )
    Assert-GateFails -Name 'scope enum requires a const declaration' -Root $scopeWrongDeclaration -Rule 'PT_SECRET_ASSIGNMENT'

    $sourceBytesLiteral = New-CleanFixture -Name 'source bytes literal'
    Write-TestText -Path (Join-Path $sourceBytesLiteral 'internal/fixture/bad.go') -Text (
        "package fixture`n`nvar " + $sourceBytesName + " = []byte(`"hardcoded-value`")`n"
    )
    Assert-GateFails -Name 'ordinary credential bytes cannot inherit maximum metadata exemption' -Root $sourceBytesLiteral -Rule 'PT_SECRET_ASSIGNMENT'

    $jsonHardcodedCredential = New-CleanFixture -Name 'json hardcoded credential'
    Write-TestText -Path (Join-Path $jsonHardcodedCredential 'schemas/credential.json') -Text ('{"' + $fieldWordB + '":"hardcoded-value"}')
    Assert-GateFails -Name 'JSON direct hardcoded credential literal' -Root $jsonHardcodedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $urlFieldA = Join-Codes @(65, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 85, 82, 76)
    $urlFieldB = Join-Codes @(84, 111, 107, 101, 110, 85, 82, 76)
    $safeCredentialURLs = New-CleanFixture -Name 'safe credential endpoint metadata'
    $safeCredentialURLsText =
        '{"' + $urlFieldA + '":"https://login.example.invalid/authorize","' +
        $urlFieldB + '":"https://login.example.invalid/token"}'
    Write-TestText -Path (Join-Path $safeCredentialURLs 'schemas/endpoints.json') -Text $safeCredentialURLsText
    Assert-GatePasses -Name 'CamelCase credential endpoint metadata permits clean public URLs' -Root $safeCredentialURLs

    $queryFieldName = Join-Codes @(115, 101, 99, 114, 101, 116)
    $unsafeCredentialURL = New-CleanFixture -Name 'unsafe encoded credential endpoint query'
    $unsafeCredentialURLText =
        '{"' + $urlFieldB + '":"https://login.example.invalid/token?' +
        $queryFieldName.Substring(0, 2) + '%63' + $queryFieldName.Substring(3) + '=hardcoded-value"}'
    Write-TestText -Path (Join-Path $unsafeCredentialURL 'schemas/endpoints.json') -Text $unsafeCredentialURLText
    Assert-GateFails -Name 'encoded credential query key fails closed' -Root $unsafeCredentialURL -Rule 'PT_SECRET_ASSIGNMENT'

    $unsafeDoubleEncodedURL = New-CleanFixture -Name 'unsafe double encoded credential endpoint query'
    $unsafeDoubleEncodedURLText =
        '{"' + $urlFieldB + '":"https://login.example.invalid/token?' +
        $queryFieldName.Substring(0, 2) + '%2563' + $queryFieldName.Substring(3) + '=hardcoded-value"}'
    Write-TestText -Path (Join-Path $unsafeDoubleEncodedURL 'schemas/endpoints.json') -Text $unsafeDoubleEncodedURLText
    Assert-GateFails -Name 'double encoded credential query key fails closed' -Root $unsafeDoubleEncodedURL -Rule 'PT_SECRET_ASSIGNMENT'

    $unsafeMultiEncodedURL = New-CleanFixture -Name 'unsafe multi encoded credential endpoint query'
    $unsafeMultiEncodedURLText =
        '{"' + $urlFieldB + '":"https://login.example.invalid/token?' +
        $queryFieldName.Substring(0, 2) + '%252563' + $queryFieldName.Substring(3) + '=hardcoded-value"}'
    Write-TestText -Path (Join-Path $unsafeMultiEncodedURL 'schemas/endpoints.json') -Text $unsafeMultiEncodedURLText
    Assert-GateFails -Name 'multi encoded credential query key fails closed' -Root $unsafeMultiEncodedURL -Rule 'PT_SECRET_ASSIGNMENT'

    $excessiveEncodedURL = New-CleanFixture -Name 'excessively encoded endpoint query'
    $encodedTail = '63'
    for ($encodingDepth = 0; $encodingDepth -lt 9; $encodingDepth++) {
        $encodedTail = '25' + $encodedTail
    }
    $excessiveEncodedURLText =
        '{"' + $urlFieldB + '":"https://login.example.invalid/token?' +
        $queryFieldName.Substring(0, 2) + '%' + $encodedTail + $queryFieldName.Substring(3) + '=hardcoded-value"}'
    Write-TestText -Path (Join-Path $excessiveEncodedURL 'schemas/endpoints.json') -Text $excessiveEncodedURLText
    Assert-GateFails -Name 'encoding deeper than the bounded decoder fails closed' -Root $excessiveEncodedURL -Rule 'PT_SECRET_ASSIGNMENT'

    $safeEncodedURL = New-CleanFixture -Name 'safe encoded endpoint query'
    $safeEncodedURLText =
        '{"' + $urlFieldB + '":"https://login.example.invalid/token?state=hello%20world"}'
    Write-TestText -Path (Join-Path $safeEncodedURL 'schemas/endpoints.json') -Text $safeEncodedURLText
    Assert-GatePasses -Name 'ordinary encoded endpoint query remains valid' -Root $safeEncodedURL

    $goCredentialURLs = New-CleanFixture -Name 'go credential endpoint metadata'
    $goCredentialURLsText =
        "package fixture`n`ntype OAuth struct { " + $urlFieldA + " string; " + $urlFieldB + " string }`n" +
        "var safe = OAuth{" + $urlFieldA + ": `"https://login.example.invalid/authorize`", " +
        $urlFieldB + ": `"https://login.example.invalid/token`",}`n"
    Write-TestText -Path (Join-Path $goCredentialURLs 'internal/fixture/safe.go') -Text $goCredentialURLsText
    Assert-GatePasses -Name 'Go OAuth endpoint metadata handles trailing composite commas' -Root $goCredentialURLs

    $goCredentialURLNearMiss = New-CleanFixture -Name 'go credential endpoint query near miss'
    $goCredentialURLNearMissText = $goCredentialURLsText.Replace(
        'https://login.example.invalid/token',
        'https://login.example.invalid/token?' + $queryFieldName + '=hardcoded-value'
    )
    Write-TestText -Path (Join-Path $goCredentialURLNearMiss 'internal/fixture/bad.go') -Text $goCredentialURLNearMissText
    Assert-GateFails -Name 'Go OAuth endpoint sensitive query fails closed after comma trimming' -Root $goCredentialURLNearMiss -Rule 'PT_SECRET_ASSIGNMENT'

    $jsonReferenceContainer = New-CleanFixture -Name 'json reference container raw value'
    Write-TestText -Path (Join-Path $jsonReferenceContainer 'schemas/credential.json') -Text ('{"' + $fieldWordA + '_refs":["hardcoded-value"]}')
    Assert-GateFails -Name 'JSON reference container cannot hide a raw literal' -Root $jsonReferenceContainer -Rule 'PT_SECRET_ASSIGNMENT'

    $safeSchemaReference = New-CleanFixture -Name 'safe local schema credential reference'
    $safeSchemaSource = Join-Path (Split-Path -Parent $PSScriptRoot) 'schemas/config/v1alpha1/workspace.schema.json'
    if (-not (Test-Path -LiteralPath $safeSchemaSource -PathType Leaf)) {
        throw 'The checked-in public SecretRef schema is missing.'
    }
    $fixtureJsonEscape = Join-Codes @(92)
    $fixtureSourceEscape = Join-Codes @(92, 92)
    $safeSchemaReferenceText = [IO.File]::ReadAllText($safeSchemaSource).
        Replace(
            '^[^' + $fixtureSourceEscape + 'x00-' + $fixtureSourceEscape + 'x1f' + $fixtureSourceEscape + 'x7f]+$',
            '^[^' + $fixtureJsonEscape + 'u0000-' + $fixtureJsonEscape + 'u001f' + $fixtureJsonEscape + 'u007f]+$'
        ).
        Replace(
            '^[^' + $fixtureSourceEscape + 'x00-' + $fixtureSourceEscape + 'x08' + $fixtureSourceEscape + 'x0e-' + $fixtureSourceEscape + 'x1f' + $fixtureSourceEscape + 'x7f]+$',
            '^[^' + $fixtureJsonEscape + 'u0000-' + $fixtureJsonEscape + 'u0008' + $fixtureJsonEscape + 'u000e-' + $fixtureJsonEscape + 'u001f' + $fixtureJsonEscape + 'u007f]+$'
        )
    Write-TestText -Path (Join-Path $safeSchemaReference 'schemas/safe.schema.json') -Text $safeSchemaReferenceText
    Assert-GatePasses -Name 'validated exact public SecretRef schema graph' -Root $safeSchemaReference

    $duplicateRootSchema = New-CleanFixture -Name 'schema duplicate root property'
    $duplicateRootSchemaText = $safeSchemaReferenceText.Insert(
        1,
        "`n  " + '"$schema": "https://attacker.invalid/schema",'
    )
    Write-TestText -Path (Join-Path $duplicateRootSchema 'schemas/duplicate-root.schema.json') -Text $duplicateRootSchemaText
    Assert-GateFails -Name 'schema duplicate root property disables all credential safe lines' -Root $duplicateRootSchema -Rule 'PT_SECRET_ASSIGNMENT'

    $singleReferenceDefinitionName = Join-Codes @(115, 101, 99, 114, 101, 116, 82, 101, 102)
    $definitionNeedle = '    "' + $singleReferenceDefinitionName + '": {'
    $definitionIndex = $safeSchemaReferenceText.IndexOf($definitionNeedle, [StringComparison]::Ordinal)
    if ($definitionIndex -lt 0) { throw 'SELFTEST_REFERENCE_DEFINITION_MISSING' }

    $duplicateDefinitionSchema = New-CleanFixture -Name 'schema duplicate definition property'
    $duplicateDefinitionPrefix =
        '    "' + $singleReferenceDefinitionName + '": {' + "`n" +
        '      "type": "string"' + "`n" +
        "    },`n"
    $duplicateDefinitionSchemaText = $safeSchemaReferenceText.Insert($definitionIndex, $duplicateDefinitionPrefix)
    Write-TestText -Path (Join-Path $duplicateDefinitionSchema 'schemas/duplicate-definition.schema.json') -Text $duplicateDefinitionSchemaText
    Assert-GateFails -Name 'schema duplicate definition property disables all credential safe lines' -Root $duplicateDefinitionSchema -Rule 'PT_SECRET_ASSIGNMENT'

    $innerDefinitionNeedle = '        "version": {'
    $innerDefinitionIndex = $safeSchemaReferenceText.IndexOf(
        $innerDefinitionNeedle,
        $definitionIndex,
        [StringComparison]::Ordinal
    )
    if ($innerDefinitionIndex -lt 0) { throw 'SELFTEST_REFERENCE_INNER_PROPERTY_MISSING' }
    $duplicateInnerSchema = New-CleanFixture -Name 'schema duplicate inner property'
    $duplicateInnerPrefix =
        '        "version": {' + "`n" +
        '          "type": "string"' + "`n" +
        "        },`n"
    $duplicateInnerSchemaText = $safeSchemaReferenceText.Insert($innerDefinitionIndex, $duplicateInnerPrefix)
    Write-TestText -Path (Join-Path $duplicateInnerSchema 'schemas/duplicate-inner.schema.json') -Text $duplicateInnerSchemaText
    Assert-GateFails -Name 'schema duplicate nested property disables all credential safe lines' -Root $duplicateInnerSchema -Rule 'PT_SECRET_ASSIGNMENT'

    $escapedDuplicateRootSchema = New-CleanFixture -Name 'schema escaped duplicate root property'
    $escapedRootName = '$schem\u0061'
    $escapedDuplicateRootSchemaText = $safeSchemaReferenceText.Insert(
        1,
        "`n  `"" + $escapedRootName + "`": `"https://attacker.invalid/schema`","
    )
    Write-TestText -Path (Join-Path $escapedDuplicateRootSchema 'schemas/escaped-duplicate-root.schema.json') -Text $escapedDuplicateRootSchemaText
    Assert-GateFails -Name 'schema escaped-equivalent duplicate property fails closed' -Root $escapedDuplicateRootSchema -Rule 'PT_SECRET_ASSIGNMENT'

    $evilSchemaReference = New-CleanFixture -Name 'schema credential reference raw default'
    $evilSchemaReferenceText =
        "{`n  `"`$schema`": `"https://json-schema.org/draft/2020-12/schema`",`n  `"properties`": {`n    `"" +
        $fieldWordA + "_ref`": {`n      `"`$ref`": `"#/`$defs/EvilRef`"`n    }`n  },`n  `"`$defs`": {`n" +
        "    `"EvilRef`": {`"type`": `"string`", `"default`": `"hardcoded-value`"}`n  }`n}`n"
    Write-TestText -Path (Join-Path $evilSchemaReference 'schemas/evil.schema.json') -Text $evilSchemaReferenceText
    Assert-GateFails -Name 'local schema credential reference rejects raw default' -Root $evilSchemaReference -Rule 'PT_SECRET_ASSIGNMENT'

    $externalSchemaReference = New-CleanFixture -Name 'external schema credential reference'
    $externalSchemaReferenceText =
        "{`n  `"`$schema`": `"https://json-schema.org/draft/2020-12/schema`",`n  `"properties`": {`n    `"" +
        $fieldWordA + "_ref`": {`n      `"`$ref`": `"https://example.invalid/schema.json`"`n    }`n  }`n}`n"
    Write-TestText -Path (Join-Path $externalSchemaReference 'schemas/external.schema.json') -Text $externalSchemaReferenceText
    Assert-GateFails -Name 'external schema credential reference fails closed' -Root $externalSchemaReference -Rule 'PT_SECRET_ASSIGNMENT'

    $cyclicSchemaReference = New-CleanFixture -Name 'cyclic schema credential reference'
    $cyclicSchemaReferenceText =
        "{`n  `"`$schema`": `"https://json-schema.org/draft/2020-12/schema`",`n  `"properties`": {`n    `"" +
        $fieldWordA + "_ref`": {`n      `"`$ref`": `"#/`$defs/A`"`n    }`n  },`n  `"`$defs`": {`n" +
        "    `"A`": {`"`$ref`": `"#/`$defs/B`"},`n    `"B`": {`"`$ref`": `"#/`$defs/A`"}`n  }`n}`n"
    Write-TestText -Path (Join-Path $cyclicSchemaReference 'schemas/cycle.schema.json') -Text $cyclicSchemaReferenceText
    Assert-GateFails -Name 'cyclic schema credential reference fails closed' -Root $cyclicSchemaReference -Rule 'PT_SECRET_ASSIGNMENT'

    $yamlHardcodedCredential = New-CleanFixture -Name 'yaml hardcoded credential'
    Write-TestText -Path (Join-Path $yamlHardcodedCredential 'docs/credential.yml') -Text ($fieldWordA + ": hardcoded-value`n")
    Assert-GateFails -Name 'YAML direct hardcoded credential literal' -Root $yamlHardcodedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $yamlReferenceContainer = New-CleanFixture -Name 'yaml reference container raw value'
    Write-TestText -Path (Join-Path $yamlReferenceContainer 'docs/credential.yml') -Text ($fieldWordA + "_refs: [hardcoded-value]`n")
    Assert-GateFails -Name 'YAML reference container cannot hide a raw literal' -Root $yamlReferenceContainer -Rule 'PT_SECRET_ASSIGNMENT'

    $envHardcodedCredential = New-CleanFixture -Name 'env hardcoded credential'
    Write-TestText -Path (Join-Path $envHardcodedCredential 'docs/credential.env') -Text ((Join-Codes @(65, 80, 73, 95, 75, 69, 89)) + "=hardcoded-value`n")
    Assert-GateFails -Name 'env direct hardcoded credential literal' -Root $envHardcodedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $powerShellHardcodedCredential = New-CleanFixture -Name 'powershell hardcoded credential'
    Write-TestText -Path (Join-Path $powerShellHardcodedCredential 'scripts/credential.ps1') -Text ('$' + $fieldWordA + " = `"hardcoded-value`"`n")
    Assert-GateFails -Name 'PowerShell direct hardcoded credential literal' -Root $powerShellHardcodedCredential -Rule 'PT_SECRET_ASSIGNMENT'

    $pathSensitiveCases = @(
        [pscustomobject]@{ Name = 'provider-key-path'; Rule = 'PT_SECRET_PROVIDER_KEY'; Leaf = (Join-Codes @(115,107,45)) + ('P' * 24) + '.txt' },
        [pscustomobject]@{ Name = 'provider-project-key-path'; Rule = 'PT_SECRET_PROVIDER_KEY'; Leaf = (Join-Codes @(115,107,45,112,114,111,106,45)) + ('P' * 24) + '.txt' },
        [pscustomobject]@{ Name = 'github-token-path'; Rule = 'PT_SECRET_GITHUB_TOKEN'; Leaf = (Join-Codes @(103,104,112,95)) + ('R' * 24) + '.txt' },
        [pscustomobject]@{ Name = 'slack-token-path'; Rule = 'PT_SECRET_SLACK_TOKEN'; Leaf = (Join-Codes @(120,111,120,98,45)) + ('S' * 20) + '.txt' },
        [pscustomobject]@{ Name = 'aws-key-path'; Rule = 'PT_SECRET_AWS_KEY'; Leaf = (Join-Codes @(65,75,73,65)) + ('T' * 16) + '.txt' },
        [pscustomobject]@{ Name = 'bearer-path'; Rule = 'PT_SECRET_BEARER'; Leaf = (Join-Codes @(66,101,97,114,101,114,32)) + ('U' * 20) + '.txt' },
        [pscustomobject]@{ Name = 'assignment-path'; Rule = 'PT_SECRET_ASSIGNMENT'; Leaf = 'token=' + ('V' * 20) + '.txt' }
    )
    foreach ($case in $pathSensitiveCases) {
        $root = New-CleanFixture -Name $case.Name
        Write-TestText -Path (Join-Path $root ('docs/' + $case.Leaf)) -Text 'path payload'
        Assert-GateFails -Name $case.Name -Root $root -Rule $case.Rule -ProtectedValues @([string]$case.Leaf)
    }

    $sensitiveName = New-CleanFixture -Name 'sensitive filename'
    Write-TestText -Path (Join-Path $sensitiveName 'docs/.env.production') -Text 'placeholder'
    Assert-GateFails -Name 'sensitive filename is redacted' -Root $sensitiveName -Rule 'PT_SECRET_FILE' -ProtectedValues @('.env.production')

    $unicodePath = New-CleanFixture -Name 'unicode unsafe path categories'
    $unicodeLeaf = 'unsafe' + [char]0x85 + [char]0x202e + [char]0x2028 + [char]0x2029 + '.txt'
    Write-TestText -Path (Join-Path $unicodePath $unicodeLeaf) -Text 'ordinary text'
    Assert-GateFails -Name 'Unicode control format and separator path characters are escaped' -Root $unicodePath -Rule 'PT_TOP_LEVEL_NOT_ALLOWED' -ProtectedValues @($unicodeLeaf) -RequiredOutput @('\u0085', '\u202e', '\u2028', '\u2029')

    if ($env:OS -ne 'Windows_NT') {
        $controlPath = New-CleanFixture -Name 'control path escaping'
        $controlLeaf = 'line' + [char]10 + 'name.md'
        $controlValue = (Join-Codes @(115,107,45)) + ('J' * 24)
        Write-TestText -Path (Join-Path $controlPath ('docs/' + $controlLeaf)) -Text $controlValue
        Assert-GateFails -Name 'control characters in output paths are escaped' -Root $controlPath -Rule 'PT_SECRET_PROVIDER_KEY' -ProtectedValues @($controlLeaf, $controlValue)
    }

    $runtimeDir = New-CleanFixture -Name 'runtime directory'
    [void][IO.Directory]::CreateDirectory((Join-Path $runtimeDir 'data'))
    Assert-GateFails -Name 'runtime directory' -Root $runtimeDir -Rule 'PT_RUNTIME'

    $constructionDirectories = @('.cache', '.tools', '.superpowers')
    foreach ($constructionDirectory in $constructionDirectories) {
        $root = New-CleanFixture -Name ('retired construction directory ' + $constructionDirectory)
        Write-TestText -Path (Join-Path $root ($constructionDirectory + '/harmless.txt')) -Text "fixture`n"
        Assert-GateFails -Name ('retired construction directory ' + $constructionDirectory) -Root $root -Rule 'PT_CONSTRUCTION'
    }

    $runtimeDirectories = @('bin', 'data', 'logs', 'storage', 'release-evidence')
    foreach ($runtimeDirectory in $runtimeDirectories) {
        $root = New-CleanFixture -Name ('retired runtime directory ' + $runtimeDirectory)
        Write-TestText -Path (Join-Path $root ($runtimeDirectory + '/harmless.txt')) -Text "fixture`n"
        Assert-GateFails -Name ('retired runtime directory ' + $runtimeDirectory) -Root $root -Rule 'PT_RUNTIME'
    }

    $runtimeFile = New-CleanFixture -Name 'runtime database'
    Write-TestText -Path (Join-Path $runtimeFile 'testdata/state.sqlite') -Text 'state'
    Assert-GateFails -Name 'runtime database' -Root $runtimeFile -Rule 'PT_RUNTIME'

    $runtimeVariants = New-CleanFixture -Name 'runtime variants'
    Write-TestText -Path (Join-Path $runtimeVariants 'testdata/state.sqlite3') -Text 'state'
    Write-TestText -Path (Join-Path $runtimeVariants 'docs/local-config.json') -Text '{}'
    Assert-GateFails -Name 'runtime extension and local config' -Root $runtimeVariants -Rule 'PT_RUNTIME'

    $binaryExtension = New-CleanFixture -Name 'binary extension'
    Write-TestText -Path (Join-Path $binaryExtension 'testdata/tool.exe') -Text 'plain text disguise'
    Assert-GateFails -Name 'binary extension' -Root $binaryExtension -Rule 'PT_BINARY'

    $binaryExtendedExtension = New-CleanFixture -Name 'expanded binary extension'
    Write-TestText -Path (Join-Path $binaryExtendedExtension 'testdata/font.woff2') -Text 'plain text disguise'
    Assert-GateFails -Name 'expanded common binary extension' -Root $binaryExtendedExtension -Rule 'PT_BINARY'

    $unknownBin = New-CleanFixture -Name 'unknown bin with control byte'
    [void][IO.Directory]::CreateDirectory((Join-Path $unknownBin 'testdata'))
    [IO.File]::WriteAllBytes((Join-Path $unknownBin 'testdata/payload.bin'), [byte[]]@(0x41,0x1b,0x42))
    Assert-GateFails -Name 'bin file with control byte' -Root $unknownBin -Rule 'PT_BINARY'

    $controlByte = New-CleanFixture -Name 'control byte in unknown extension'
    [void][IO.Directory]::CreateDirectory((Join-Path $controlByte 'testdata'))
    [IO.File]::WriteAllBytes((Join-Path $controlByte 'testdata/payload.unknown'), [byte[]]@(0x41,0x1b,0x42))
    Assert-GateFails -Name 'C0 control byte outside tab newline carriage return' -Root $controlByte -Rule 'PT_BINARY'

    $c1Control = New-CleanFixture -Name 'unicode c1 control'
    Write-TestText -Path (Join-Path $c1Control 'docs/control.md') -Text ('before' + [char]0x85 + 'after')
    Assert-GateFails -Name 'decoded C1 control character' -Root $c1Control -Rule 'PT_BINARY'

    $binaryMagic = New-CleanFixture -Name 'binary magic'
    [void][IO.Directory]::CreateDirectory((Join-Path $binaryMagic 'testdata'))
    [IO.File]::WriteAllBytes((Join-Path $binaryMagic 'testdata/payload.txt'), [byte[]]@(0x7f,0x45,0x4c,0x46,0x01))
    Assert-GateFails -Name 'binary magic' -Root $binaryMagic -Rule 'PT_BINARY'

    $expandedMagic = New-CleanFixture -Name 'expanded binary magic'
    [void][IO.Directory]::CreateDirectory((Join-Path $expandedMagic 'testdata'))
    [IO.File]::WriteAllBytes((Join-Path $expandedMagic 'testdata/disguised.txt'), [byte[]]@(0x25,0x50,0x44,0x46,0x2d,0x31,0x2e,0x37))
    Assert-GateFails -Name 'expanded common binary magic' -Root $expandedMagic -Rule 'PT_BINARY'

    $archiveMagic = New-CleanFixture -Name 'ar archive magic'
    [void][IO.Directory]::CreateDirectory((Join-Path $archiveMagic 'testdata'))
    [IO.File]::WriteAllBytes((Join-Path $archiveMagic 'testdata/disguised.txt'), [Text.Encoding]::ASCII.GetBytes("!<arch>`n"))
    Assert-GateFails -Name 'ar archive magic is rejected despite a text extension' -Root $archiveMagic -Rule 'PT_BINARY'

    $portableHole = New-CleanFixture -Name 'portable sparse hole content'
    $holePath = Join-Path $portableHole 'testdata/hole.txt'
    [void][IO.Directory]::CreateDirectory((Split-Path -Parent $holePath))
    $holeStream = [IO.File]::Open($holePath, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $holeStream.Position = 4096
        $holeStream.WriteByte(0x41)
    } finally {
        $holeStream.Dispose()
    }
    Assert-GateFails -Name 'portable sparse hole is rejected through NUL content' -Root $portableHole -Rule 'PT_BINARY'

    if ($env:OS -ne 'Windows_NT') {
        $fifoFixture = New-CleanFixture -Name 'unix fifo'
        $fifoPath = Join-Path $fifoFixture 'testdata/never-read.fifo'
        [void][IO.Directory]::CreateDirectory((Split-Path -Parent $fifoPath))
        $mkfifoPath = '/usr/bin/mkfifo'
        if (-not (Test-Path -LiteralPath $mkfifoPath -PathType Leaf)) { throw 'Required Unix mkfifo fixture tool is missing.' }
        & $mkfifoPath '--' $fifoPath 2>$null
        if ($LASTEXITCODE -ne 0) { throw 'Unable to create Unix FIFO fixture.' }
        Assert-GateFails -Name 'Unix FIFO is classified before any content read' -Root $fifoFixture -Rule 'PT_FILE_SPECIAL'
    }

    if ($env:OS -eq 'Windows_NT') {
        $sparseAttribute = New-CleanFixture -Name 'windows sparse attribute'
        $sparsePath = Join-Path $sparseAttribute 'testdata/sparse.txt'
        Write-TestText -Path $sparsePath -Text 'valid text without a hole'
        if ((Try-MarkSparseFile -Path $sparsePath) -and (((Get-Item -LiteralPath $sparsePath).Attributes -band [IO.FileAttributes]::SparseFile) -ne 0)) {
            Assert-GateFails -Name 'windows sparse file attribute' -Root $sparseAttribute -Rule 'PT_FILE_SPECIAL'
        }
    }

    $construction = New-CleanFixture -Name 'construction docs'
    Write-TestText -Path (Join-Path $construction 'docs/superpowers/specs/work.md') -Text 'x'
    Assert-GateFails -Name 'construction docs' -Root $construction -Rule 'PT_CONSTRUCTION'

    $constructionCase = New-CleanFixture -Name 'construction case variant'
    Write-TestText -Path (Join-Path $constructionCase 'docs/SuperPowers/specs/work.md') -Text 'x'
    Assert-GateFails -Name 'construction path case variant' -Root $constructionCase -Rule 'PT_CONSTRUCTION'

    $retiredConstructionNames = @(Get-RetiredConstructionScriptNames)
    for ($retiredIndex = 0; $retiredIndex -lt $retiredConstructionNames.Count; $retiredIndex++) {
        $constructionScript = New-CleanFixture -Name ('construction script original path ' + $retiredIndex)
        Write-TestText -Path (Join-Path $constructionScript ('scripts/' + $retiredConstructionNames[$retiredIndex])) -Text 'x'
        Assert-GateFails -Name ('construction script original path ' + $retiredIndex) -Root $constructionScript -Rule 'PT_CONSTRUCTION'

        $relocatedConstructionScript = New-CleanFixture -Name ('construction script relocated ' + $retiredIndex)
        Write-TestText -Path (Join-Path $relocatedConstructionScript ('docs/archive/' + $retiredConstructionNames[$retiredIndex])) -Text 'x'
        Assert-GateFails -Name ('construction script relocated ' + $retiredIndex) -Root $relocatedConstructionScript -Rule 'PT_CONSTRUCTION'
    }

    for ($retiredIndex = 0; $retiredIndex -lt $retiredConstructionNames.Count; $retiredIndex++) {
        $constructionReference = New-CleanFixture -Name ('retired construction reference ' + $retiredIndex)
        $referenceText = if (($retiredIndex % 2) -eq 0) {
            'retired reference: scripts/' + $retiredConstructionNames[$retiredIndex]
        } else {
            'retired reference: ' + $retiredConstructionNames[$retiredIndex]
        }
        Write-TestText -Path (Join-Path $constructionReference 'docs/reference.md') -Text ($referenceText + "`n")
        Assert-GateFails -Name ('retired construction text reference ' + $retiredIndex) -Root $constructionReference -Rule 'PT_CONSTRUCTION'
    }

    $constructionNearMatch = New-CleanFixture -Name 'retired construction reference boundaries'
    $nearMatchLines = @($retiredConstructionNames | ForEach-Object { ('x' + $_) + "`n" + ($_ + 'x') })
    Write-TestText -Path (Join-Path $constructionNearMatch 'docs/reference.md') -Text (($nearMatchLines -join "`n") + "`n")
    Assert-GatePasses -Name 'retired construction reference matcher uses complete filename boundaries' -Root $constructionNearMatch

    $retiredBatchArtifacts = @(Get-RetiredBatchBArtifactNames)
    for ($artifactIndex = 0; $artifactIndex -lt $retiredBatchArtifacts.Count; $artifactIndex++) {
        $retiredArtifact = $retiredBatchArtifacts[$artifactIndex]
        $artifactRoot = New-CleanFixture -Name ('retired batch b artifact ' + $artifactIndex)
        if ($retiredArtifact.EndsWith('.json', [StringComparison]::OrdinalIgnoreCase) -or $retiredArtifact.EndsWith('.txt', [StringComparison]::OrdinalIgnoreCase)) {
            Write-TestText -Path (Join-Path $artifactRoot ('testdata/release/' + $retiredArtifact)) -Text "sanitized fixture`n"
        } else {
            Write-TestText -Path (Join-Path $artifactRoot ('testdata/release/' + $retiredArtifact + '/manifest.json')) -Text "{}`n"
        }
        Assert-GateFails -Name ('retired batch b artifact ' + $artifactIndex) -Root $artifactRoot -Rule 'PT_RUNTIME'
    }

    $genericBundle = New-CleanFixture -Name 'generic optional module bundle remains allowed'
    Write-TestText -Path (Join-Path $genericBundle 'testdata/release/module-export.bundle/manifest.json') -Text "{}`n"
    Write-TestText -Path (Join-Path $genericBundle 'testdata/release/rehearsal-evidence.json.example') -Text "sanitized example`n"
    Assert-GatePasses -Name 'unrelated optional module bundle and near-match remain allowed' -Root $genericBundle

    $designConstruction = New-CleanFixture -Name 'design construction'
    Write-TestText -Path (Join-Path $designConstruction 'docs/design/work.md') -Text 'x'
    Assert-GateFails -Name 'legacy design construction directory' -Root $designConstruction -Rule 'PT_CONSTRUCTION'

    $generatorConstruction = New-CleanFixture -Name 'generator construction'
    Write-TestText -Path (Join-Path $generatorConstruction 'cmd/public-baseline-gen/main.go') -Text "package main`n"
    Assert-GateFails -Name 'public baseline generator construction directory' -Root $generatorConstruction -Rule 'PT_CONSTRUCTION'

    $privateConstructionAssets = @(
        'DEEPSEEK_BENCHMARK_2026-07-15.md',
        'DEEPSEEK_BENCHMARK_RERUN_2026-07-15.md'
    )
    foreach ($privateConstructionAsset in $privateConstructionAssets) {
        $privateAsset = New-CleanFixture -Name ('exact private asset ' + $privateConstructionAsset)
        Write-TestText -Path (Join-Path $privateAsset ('docs/' + $privateConstructionAsset)) -Text 'x'
        Assert-GateFails -Name ('exact private construction asset ' + $privateConstructionAsset) -Root $privateAsset -Rule 'PT_CONSTRUCTION'
    }

    foreach ($retired in @((Join-Codes @(120,120,98)), (Join-Codes @(99,104,117,110,107,98,117,114,115,116)))) {
        $root = New-CleanFixture -Name 'retired identity'
        Write-TestText -Path (Join-Path $root 'docs/identity.md') -Text ('retired=' + $retired.ToUpperInvariant())
        Assert-GateFails -Name 'retired identity' -Root $root -Rule 'PT_OLD_IDENTITY'
    }

    $php = New-CleanFixture -Name 'legacy php'
    Write-TestText -Path (Join-Path $php 'examples/legacy.php') -Text '<?php echo 1;'
    Assert-GateFails -Name 'legacy php' -Root $php -Rule 'PT_LEGACY_SOURCE'

    $legacyImport = New-CleanFixture -Name 'legacy import'
    Write-TestText -Path (Join-Path $legacyImport 'internal/legacy/legacy.go') -Text "package legacy`nimport `"freeagent/old`"`n"
    Assert-GateFails -Name 'legacy go import' -Root $legacyImport -Rule 'PT_LEGACY_SOURCE'

    $legacyExactImport = New-CleanFixture -Name 'exact legacy import'
    Write-TestText -Path (Join-Path $legacyExactImport 'internal/legacy/exact.go') -Text "package legacy`nimport `"freeagent`"`n"
    Assert-GateFails -Name 'exact legacy Go import' -Root $legacyExactImport -Rule 'PT_LEGACY_SOURCE'

    $legacyImportGroup = New-CleanFixture -Name 'legacy grouped imports'
    $tick = [char]96
    $legacyGroupSource = "package legacy`nimport (`n  alias `"freeagent/alias`"`n  _ $tick" + 'freeagent/raw' + "$tick`n  . `"freeagent/dot`"`n)`n"
    Write-TestText -Path (Join-Path $legacyImportGroup 'internal/legacy/group.go') -Text $legacyGroupSource
    Assert-GateFails -Name 'legacy alias blank dot raw grouped imports' -Root $legacyImportGroup -Rule 'PT_LEGACY_SOURCE' -ProtectedValues @('freeagent/raw')

    $legacyImportAcrossLines = New-CleanFixture -Name 'legacy import across lines and comments'
    $legacyAcrossLinesSource = "package legacy`nimport alias /* comment`ncontinues */`n`"freeagent/cross-line`"`n"
    Write-TestText -Path (Join-Path $legacyImportAcrossLines 'internal/legacy/cross.go') -Text $legacyAcrossLinesSource
    Assert-GateFails -Name 'legacy import tracks alias across lines and block comments' -Root $legacyImportAcrossLines -Rule 'PT_LEGACY_SOURCE'

    $wrongModule = New-CleanFixture -Name 'wrong module'
    Write-TestText -Path (Join-Path $wrongModule 'go.mod') -Text "module example.invalid/wrong`n"
    Assert-GateFails -Name 'wrong module identity' -Root $wrongModule -Rule 'PT_GO_MOD_IDENTITY'

    $missingModule = New-CleanFixture -Name 'missing module file'
    [IO.File]::Delete((Join-Path $missingModule 'go.mod'))
    Assert-GateFails -Name 'missing go.mod' -Root $missingModule -Rule 'PT_GO_MOD_MISSING'

    $tagAction = New-CleanFixture -Name 'mutable action tag'
    Write-TestText -Path (Join-Path $tagAction '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: actions/checkout@v4`n"
    Assert-GateFails -Name 'mutable action tag' -Root $tagAction -Rule 'PT_ACTION_REF'

    $shortAction = New-CleanFixture -Name 'short action sha'
    Write-TestText -Path (Join-Path $shortAction '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: actions/checkout@1234567`n"
    Assert-GateFails -Name 'short action sha' -Root $shortAction -Rule 'PT_ACTION_REF'

    $dynamicAction = New-CleanFixture -Name 'dynamic action ref'
    $dynamic = '$' + '{{ matrix.action }}'
    Write-TestText -Path (Join-Path $dynamicAction '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: $dynamic`n"
    Assert-GateFails -Name 'dynamic action ref' -Root $dynamicAction -Rule 'PT_ACTION_REF'

    $quotedMutable = New-CleanFixture -Name 'quoted mutable action'
    Write-TestText -Path (Join-Path $quotedMutable '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - 'uses': 'actions/checkout@v4'`n"
    Assert-GateFails -Name 'quoted uses key mutable ref' -Root $quotedMutable -Rule 'PT_ACTION_REF'

    $escapedKey = New-CleanFixture -Name 'escaped uses key'
    Write-TestText -Path (Join-Path $escapedKey '.github/workflows/gate.yml') -Text ('name: gate' + "`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - `"u\u0073es`": actions/checkout@" + ('1' * 40) + "`n")
    Assert-GateFails -Name 'escaped quoted key fails closed' -Root $escapedKey -Rule 'PT_ACTION_YAML'

    $flowMapping = New-CleanFixture -Name 'flow mapping action'
    Write-TestText -Path (Join-Path $flowMapping '.github/workflows/gate.yml') -Text ("name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - { uses: actions/checkout@" + ('1' * 40) + " }`n")
    Assert-GateFails -Name 'flow mapping is rejected fail closed' -Root $flowMapping -Rule 'PT_ACTION_YAML'

    $flowSequenceFirst = New-CleanFixture -Name 'flow sequence first implicit mapping'
    Write-TestText -Path (Join-Path $flowSequenceFirst '.github/workflows/gate.yml') -Text ("name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps: [ uses: actions/checkout@" + ('1' * 40) + " ]`n")
    Assert-GateFails -Name 'flow sequence first implicit mapping is rejected' -Root $flowSequenceFirst -Rule 'PT_ACTION_YAML'

    $flowSequenceQuoted = New-CleanFixture -Name 'flow sequence quoted implicit mapping'
    Write-TestText -Path (Join-Path $flowSequenceQuoted '.github/workflows/gate.yml') -Text ("name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps: [ 'uses': 'actions/checkout@" + ('1' * 40) + "' ]`n")
    Assert-GateFails -Name 'flow sequence quoted implicit mapping is rejected' -Root $flowSequenceQuoted -Rule 'PT_ACTION_YAML'

    $flowSequenceNested = New-CleanFixture -Name 'flow sequence nested implicit mapping'
    Write-TestText -Path (Join-Path $flowSequenceNested '.github/workflows/gate.yml') -Text ("name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps: [ [ `"uses`": `"actions/checkout@" + ('1' * 40) + "`" ] ]`n")
    Assert-GateFails -Name 'flow sequence nested implicit mapping is rejected' -Root $flowSequenceNested -Rule 'PT_ACTION_YAML'

    $duplicateUses = New-CleanFixture -Name 'duplicate uses key'
    Write-TestText -Path (Join-Path $duplicateUses '.github/workflows/gate.yml') -Text ("name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - uses: actions/checkout@" + ('1' * 40) + "`n        'uses': actions/checkout@" + ('1' * 40) + "`n")
    Assert-GateFails -Name 'duplicate quoted and plain uses keys' -Root $duplicateUses -Rule 'PT_ACTION_YAML'

    $blockUses = New-CleanFixture -Name 'block uses value'
    Write-TestText -Path (Join-Path $blockUses '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: |`n      actions/checkout@v4`n"
    Assert-GateFails -Name 'block scalar uses value' -Root $blockUses -Rule 'PT_ACTION_REF'

    $unknownUses = New-CleanFixture -Name 'unknown uses context'
    Write-TestText -Path (Join-Path $unknownUses '.github/workflows/gate.yml') -Text ("name: gate`nuses: actions/checkout@" + ('1' * 40) + "`n")
    Assert-GateFails -Name 'uses outside executable schema context' -Root $unknownUses -Rule 'PT_ACTION_YAML'

    $escapingLocalAction = New-CleanFixture -Name 'escaping local action'
    Write-TestText -Path (Join-Path $escapingLocalAction '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: ./../outside`n"
    Assert-GateFails -Name 'escaping local action' -Root $escapingLocalAction -Rule 'PT_ACTION_REF'

    $missingLocalAction = New-CleanFixture -Name 'missing local action manifest'
    [void][IO.Directory]::CreateDirectory((Join-Path $missingLocalAction '.github/actions/missing'))
    Write-TestText -Path (Join-Path $missingLocalAction '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - uses: ./.github/actions/missing`n"
    Assert-GateFails -Name 'local action requires one exact manifest' -Root $missingLocalAction -Rule 'PT_ACTION_REF'

    $wrongLocalType = New-CleanFixture -Name 'wrong local uses type'
    Write-TestText -Path (Join-Path $wrongLocalType '.github/actions/local/action.yml') -Text "name: local`nruns:`n  using: composite`n  steps:`n    - run: echo ok`n      shell: bash`n"
    Write-TestText -Path (Join-Path $wrongLocalType '.github/workflows/gate.yml') -Text "name: gate`non: [push]`njobs:`n  x:`n    uses: ./.github/actions/local`n"
    Assert-GateFails -Name 'job cannot use local action directory' -Root $wrongLocalType -Rule 'PT_ACTION_REF'

    $compositeMutable = New-CleanFixture -Name 'composite mutable action'
    Write-TestText -Path (Join-Path $compositeMutable '.github/actions/local/action.yml') -Text "name: local`nruns:`n  using: composite`n  steps:`n    - uses: actions/checkout@v4`n"
    Assert-GateFails -Name 'composite action uses must be pinned' -Root $compositeMutable -Rule 'PT_ACTION_REF'

    $duplicateUsing = New-CleanFixture -Name 'duplicate action using'
    Write-TestText -Path (Join-Path $duplicateUsing '.github/actions/local/action.yml') -Text "name: local`nruns:`n  using: composite`n  'using': composite`n  steps:`n    - run: echo ok`n      shell: bash`n"
    Assert-GateFails -Name 'duplicate action metadata key' -Root $duplicateUsing -Rule 'PT_ACTION_YAML'

    $driftAction = New-CleanFixture -Name 'action drift'
    $firstSha = 'a' * 40
    $secondSha = 'b' * 40
    Write-TestText -Path (Join-Path $driftAction '.github/workflows/other.yml') -Text "name: other`non: [push]`njobs:`n  x:`n    runs-on: ubuntu-latest`n    steps:`n      - uses: actions/checkout@$firstSha`n      - uses: actions/checkout@$secondSha`n"
    Assert-GateFails -Name 'same action sha drift' -Root $driftAction -Rule 'PT_ACTION_DRIFT'

    $imageBinary = New-CleanFixture -Name 'image binary'
    [void][IO.Directory]::CreateDirectory((Join-Path $imageBinary 'docs/assets'))
    [IO.File]::WriteAllBytes((Join-Path $imageBinary 'docs/assets/small.png'), [byte[]]@(0x89,0x50,0x4e,0x47,0x01))
    Assert-GateFails -Name 'all binary assets are forbidden in core source' -Root $imageBinary -Rule 'PT_BINARY'

    $largeText = New-CleanFixture -Name 'large text is not arbitrarily capped'
    Write-TestText -Path (Join-Path $largeText 'docs/large.md') -Text ('T' * ((5 * 1024 * 1024) + 1))
    Assert-GatePasses -Name 'large strict UTF8 text remains valid' -Root $largeText

    if ($env:OS -eq 'Windows_NT') {
        $ads = New-CleanFixture -Name 'alternate data stream'
        $adsValue = (Join-Codes @(115,107,45)) + ('H' * 24)
        Set-Content -LiteralPath (Join-Path $ads 'README.md') -Stream 'hidden' -Value $adsValue -Encoding UTF8
        Assert-GateFails -Name 'alternate data stream' -Root $ads -Rule 'PT_ADS' -ProtectedValues @($adsValue)

        $directoryAds = New-CleanFixture -Name 'directory alternate data stream'
        [void][IO.Directory]::CreateDirectory((Join-Path $directoryAds 'docs'))
        Set-Content -LiteralPath (Join-Path $directoryAds 'docs') -Stream 'hidden' -Value $adsValue -Encoding UTF8
        Assert-GateFails -Name 'directory alternate data stream' -Root $directoryAds -Rule 'PT_ADS' -ProtectedValues @($adsValue)

        $rootAds = New-CleanFixture -Name 'root alternate data stream'
        Set-Content -LiteralPath $rootAds -Stream 'hidden' -Value $adsValue -Encoding UTF8
        Assert-GateFails -Name 'root alternate data stream' -Root $rootAds -Rule 'PT_ADS' -ProtectedValues @($adsValue)
    }

    $reparse = New-CleanFixture -Name 'reparse point'
    $target = Join-Path $suiteRoot 'reparse-target'
    [void][IO.Directory]::CreateDirectory($target)
    Write-TestText -Path (Join-Path $target 'outside.txt') -Text $ignoredValue
    $link = Join-Path $reparse 'docs/linked'
    New-ReparseDirectory -Path $link -Target $target
    Assert-GateFails -Name 'reparse traversal rejected' -Root $reparse -Rule 'PT_REPARSE' -ProtectedValues @($ignoredValue)

    $ancestorFixture = New-CleanFixture -Name 'reparse ancestor target'
    $ancestorLink = Join-Path $tempRoot ('freeagent public ancestor ' + [Guid]::NewGuid().ToString('N'))
    New-ReparseDirectory -Path $ancestorLink -Target $suiteRoot
    $rootThroughAncestor = Join-Path $ancestorLink (Split-Path -Leaf $ancestorFixture)
    Assert-GateFails -Name 'lexical reparse ancestor rejected' -Root $rootThroughAncestor -Rule 'PT_REPARSE'

    $finalTree = New-CleanFixture -Name 'expected R0-4 final file set'
    foreach ($rootDocument in @('.gitattributes', '.gitignore', 'CONTRIBUTING.md', 'LICENSE', 'SECURITY.md', 'THIRD_PARTY_NOTICES.md')) {
        Write-TestText -Path (Join-Path $finalTree $rootDocument) -Text "public release text`n"
    }
    Write-TestText -Path (Join-Path $finalTree 'docs/architecture/specs/core.md') -Text "# Core architecture`n"
    Write-TestText -Path (Join-Path $finalTree 'docs/reference/modules.md') -Text "# Optional modules`n"
    Write-TestText -Path (Join-Path $finalTree 'examples/example.go') -Text "package examples`n"
    Write-TestText -Path (Join-Path $finalTree 'internal/core/core.go') -Text "package core`n"
    Write-TestText -Path (Join-Path $finalTree 'schemas/config.json') -Text "{}`n"
    Write-TestText -Path (Join-Path $finalTree 'schemas/module-manifest.schema.json') -Text "{}`n"
    Write-TestText -Path (Join-Path $finalTree 'sdk/client.go') -Text "package sdk`n"
    Write-TestText -Path (Join-Path $finalTree 'testdata/release/manifest.json') -Text "{}`n"
    Write-TestText -Path (Join-Path $finalTree 'third_party/NOTICE.txt') -Text "audited notice`n"
    Write-TestText -Path (Join-Path $finalTree 'vendor/modules.txt') -Text "audited module metadata`n"
    [void][IO.Directory]::CreateDirectory((Join-Path $finalTree 'scripts'))
    $plannedRemovalScripts = @(Get-RetiredConstructionScriptNames)
    $permanentScripts = @(Get-ChildItem -LiteralPath $PSScriptRoot -File -Force | Where-Object { $plannedRemovalScripts -cnotcontains $_.Name } | Sort-Object Name)
    $requiredPermanentScripts = @(
        'Test-PublicTree.ps1', 'Test-PublicTree.Tests.ps1',
        'Test-License.ps1', 'Test-License.Tests.ps1',
        'Test-Docs.ps1', 'Test-Docs.Tests.ps1'
    )
    foreach ($requiredScript in $requiredPermanentScripts) {
        if (@($permanentScripts | Where-Object { $_.Name -ceq $requiredScript }).Count -ne 1) {
            throw "Required permanent release script is missing: $requiredScript"
        }
    }
    foreach ($permanentScript in $permanentScripts) {
        [IO.File]::WriteAllBytes((Join-Path $finalTree ('scripts/' + $permanentScript.Name)), [IO.File]::ReadAllBytes($permanentScript.FullName))
    }
    Assert-GatePasses -Name 'expected R0-4 final file set mechanically includes every permanent release script' -Root $finalTree

    Write-Output "PASS Test-PublicTree self-tests ($script:PassCount assertions)"
} finally {
    foreach ($link in $script:ReparsePaths) {
        try { Remove-Item -LiteralPath $link -Force -ErrorAction Stop } catch { }
    }
    $canonicalSuite = [IO.Path]::GetFullPath($suiteRoot)
    $tempPrefix = $tempRoot + [IO.Path]::DirectorySeparatorChar
    $comparison = if ($env:OS -eq 'Windows_NT') { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
    if ($canonicalSuite.StartsWith($tempPrefix, $comparison) -and [IO.Path]::GetFileName($canonicalSuite).StartsWith('freeagent public tree tests ', [StringComparison]::Ordinal)) {
        Remove-Item -LiteralPath $canonicalSuite -Recurse -Force -ErrorAction SilentlyContinue
    } else {
        throw 'Refusing unsafe test cleanup path.'
    }
}
