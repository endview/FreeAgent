[CmdletBinding(DefaultParameterSetName = 'Prepare')]
param(
    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [switch]$Prepare,

    [Parameter(Mandatory = $true, ParameterSetName = 'Initialize')]
    [switch]$Initialize,

    [Parameter(Mandatory = $true, ParameterSetName = 'Run')]
    [switch]$Run,

    [Parameter(Mandatory = $true, ParameterSetName = 'Finalize')]
    [switch]$Finalize,

    [Parameter(Mandatory = $true, ParameterSetName = 'Seal')]
    [switch]$Seal,

    [Parameter(Mandatory = $true)]
    [ValidateSet(
        'permanent',
        'linux-quality',
        'windows',
        'linux-race',
        'cross-build'
    )]
    [string]$JobKind,

    [Parameter(Mandatory = $true)][string]$BaseRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$RepositoryRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$Revision,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [Parameter(Mandatory = $true, ParameterSetName = 'Finalize')]
    [string]$GitHubOutputPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$GitHubEnvironmentPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Finalize')]
    [Parameter(Mandatory = $true, ParameterSetName = 'Seal')]
    [string]$ManifestSha256,

    [Parameter(Mandatory = $true, ParameterSetName = 'Seal')]
    [string]$ExpectedArtifactSetSha256,

    [Parameter(ParameterSetName = 'Prepare')]
    [Parameter(ParameterSetName = 'Initialize')]
    [Parameter(ParameterSetName = 'Run')]
    [Parameter(ParameterSetName = 'Finalize')]
    [Parameter(ParameterSetName = 'Seal')]
    [string]$TargetGoos = '',

    [Parameter(ParameterSetName = 'Prepare')]
    [Parameter(ParameterSetName = 'Initialize')]
    [Parameter(ParameterSetName = 'Run')]
    [Parameter(ParameterSetName = 'Finalize')]
    [Parameter(ParameterSetName = 'Seal')]
    [string]$TargetGoarch = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Utf8Strict = New-Object Text.UTF8Encoding($false, $true)
$script:IsWindowsPlatform =
    [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$script:ControllerSha256 =
    '61adff112a67b52963a9b2ebbb15ff4cc9a069f4a469a1e88ba9bedfb824aea1'
$script:BootstrapSha256 =
    'c6611cd6eb290410912fcbb1a1be9c3327ad65ddb3f7185c3dccd7016391dd9f'
$script:GeneratorSha256 =
    '87345ec4e605b5c01e535507493c198266202abf875cd2a31e849ede7934695c'
$script:VerifierSha256 =
    '3acf389912dab063f2817f3ad36293bdc2cfe403ada9f0b82d01fbb90b57bb44'
$script:CommandRunnerSha256 =
    'edec08adf204604729bcda5074ad3f7eade6e3b332da9730a7771bcb7e97f7e2'
$script:WorkflowJobSha256 =
    '7e405e7cb9f033c67f4653848d0174166e30ea381636c7097ce2982201b0a830'
$script:MaximumScriptBytes = 4194304
$script:ExactJobKinds = @(
    'permanent',
    'linux-quality',
    'windows',
    'linux-race',
    'cross-build'
)

function Fail-CIWorkflowControl {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "CI_WORKFLOW_CONTROL_FAIL code=$Code"
}

function Get-CIWCSha256 {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).
            Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Get-CIWCAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-CIWorkflowControl -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-CIWorkflowControl -Code $Code
        }
    }
    $isAbsolute = $false
    if ($script:IsWindowsPlatform) {
        $deviceProbe = $Value.Replace([char]47, [char]92)
        $isDevicePath =
            $deviceProbe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\??\', [StringComparison]::Ordinal)
        $isDriveAbsolute = $Value -match '^[A-Za-z]:[\\/]'
        $isAbsolute = -not $isDevicePath -and $isDriveAbsolute
    } else {
        $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
    }
    if (-not $isAbsolute) {
        Fail-CIWorkflowControl -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
        if ($script:IsWindowsPlatform -and (
            $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\??\', [StringComparison]::Ordinal)
        )) {
            Fail-CIWorkflowControl -Code $Code
        }
        $root = [IO.Path]::GetPathRoot($full)
        while ($full.Length -gt $root.Length -and
            ($full.EndsWith(
                [IO.Path]::DirectorySeparatorChar.ToString(),
                [StringComparison]::Ordinal
            ) -or
            $full.EndsWith(
                [IO.Path]::AltDirectorySeparatorChar.ToString(),
                [StringComparison]::Ordinal
            ))) {
            $full = $full.Substring(0, $full.Length - 1)
        }
        return $full
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_CONTROL_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowControl -Code $Code
    }
}

function Assert-CIWCNotFileSystemRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $root = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrWhiteSpace($root)) {
        Fail-CIWorkflowControl -Code $Code
    }
    $trimmedPath = $Path.TrimEnd([char[]]@([char]92, [char]47))
    $trimmedRoot = $root.TrimEnd([char[]]@([char]92, [char]47))
    if ([string]::Equals(
        $trimmedPath,
        $trimmedRoot,
        $script:PathComparison
    )) {
        Fail-CIWorkflowControl -Code $Code
    }
}

function Assert-CIWCNoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = $Path
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-CIWorkflowControl -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -ceq $cursor) {
            break
        }
        $cursor = $parent
    }
}

function Read-CIWCBoundedFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$MaximumBytes,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($MaximumBytes -lt 0) {
        Fail-CIWorkflowControl -Code $Code
    }
    Assert-CIWCNoReparseAncestry -Path $Path -Code $Code
    $stream = $null
    try {
        $item = Get-Item -LiteralPath $Path -Force
        if (-not ($item -is [IO.FileInfo]) -or
            ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-CIWorkflowControl -Code $Code
        }
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read
        )
        if ($stream.Length -gt $MaximumBytes) {
            Fail-CIWorkflowControl -Code $Code
        }
        [byte[]]$bytes = New-Object byte[] ([int]$stream.Length)
        $offset = 0
        while ($offset -lt $bytes.Length) {
            $read = $stream.Read($bytes, $offset, $bytes.Length - $offset)
            if ($read -le 0) {
                Fail-CIWorkflowControl -Code $Code
            }
            $offset += $read
        }
        if ($stream.ReadByte() -ne -1) {
            Fail-CIWorkflowControl -Code $Code
        }
        return ,$bytes
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_CONTROL_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowControl -Code $Code
    } finally {
        if ($null -ne $stream) {
            $stream.Dispose()
        }
    }
}

function Get-CIWCAuthenticatedScript {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedSha256,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($ExpectedSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIWorkflowControl -Code $Code
    }
    $full = Get-CIWCAbsolutePath -Value $Path -Code $Code
    [byte[]]$bytes = Read-CIWCBoundedFile `
        -Path $full `
        -MaximumBytes $script:MaximumScriptBytes `
        -Code $Code
    if ((Get-CIWCSha256 -Bytes $bytes) -cne $ExpectedSha256) {
        Fail-CIWorkflowControl -Code $Code
    }
    try {
        $source = $script:Utf8Strict.GetString($bytes)
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseInput(
            $source,
            [ref]$tokens,
            [ref]$errors
        )
        if ($errors.Count -ne 0) {
            Fail-CIWorkflowControl -Code $Code
        }
        $block = [ScriptBlock]::Create($source)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_CONTROL_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowControl -Code $Code
    }
    return [pscustomobject]@{
        Bytes = $bytes
        Block = $block
        Path = $full
    }
}

function Write-CIWCSnapshot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$Bytes
    )
    $stream = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stream.Write($Bytes, 0, $Bytes.Length)
        $stream.Flush($true)
    } catch {
        Fail-CIWorkflowControl -Code 'CIWC_SNAPSHOT_WRITE_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-CIWCJobContractBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Kind,
        [Parameter(Mandatory = $true)][string]$BuildContextSha256,
        [string]$Goos = '',
        [string]$Goarch = ''
    )
    if ($BuildContextSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    if ($Kind -ceq 'cross-build') {
        if ($Goos -cnotmatch '^(?:windows|linux|darwin)$' -or
            $Goarch -cnotmatch '^(?:amd64|arm64)$') {
            Fail-CIWorkflowControl -Code 'CIWC_CROSS_TARGET_INVALID'
        }
    } elseif (-not [string]::IsNullOrEmpty($Goos) -or
        -not [string]::IsNullOrEmpty($Goarch)) {
        Fail-CIWorkflowControl -Code 'CIWC_CROSS_TARGET_INVALID'
    }
    $text =
        "schema=2`n" +
        "job_kind=$Kind`n" +
        "target_goos=$Goos`n" +
        "target_goarch=$Goarch`n" +
        "build_context_sha256=$BuildContextSha256`n"
    return ,([byte[]]$script:Utf8NoBom.GetBytes($text))
}

function Get-CIWCBuildContextBytes {
    param(
        [Parameter(Mandatory = $true)][string]$RevisionValue,
        [Parameter(Mandatory = $true)][string]$CreatedUtc
    )
    if ($RevisionValue -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    $created = [DateTime]::MinValue
    if (-not [DateTime]::TryParseExact(
        $CreatedUtc,
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::AssumeUniversal -bor
            [Globalization.DateTimeStyles]::AdjustToUniversal,
        [ref]$created
    ) -or $created.ToString(
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture
    ) -cne $CreatedUtc) {
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    return ,([byte[]]$script:Utf8NoBom.GetBytes(
        "schema=1`nrevision=$RevisionValue`ncreated_utc=$CreatedUtc`n"
    ))
}

function Read-CIWCBuildContext {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        [byte[]]$bytes = Read-CIWCBoundedFile `
            -Path $Path `
            -MaximumBytes 512 `
            -Code 'CIWC_BUILD_CONTEXT_INVALID'
        $text = $script:Utf8Strict.GetString($bytes)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_CONTROL_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    $match = [regex]::Match(
        $text,
        '\Aschema=1\nrevision=((?:[0-9a-f]{40}|[0-9a-f]{64}))\n' +
            'created_utc=([0-9]{4}-[0-9]{2}-[0-9]{2}T' +
            '[0-9]{2}:[0-9]{2}:[0-9]{2}Z)\n\z'
    )
    if (-not $match.Success) {
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    [byte[]]$expected = Get-CIWCBuildContextBytes `
        -RevisionValue $match.Groups[1].Value `
        -CreatedUtc $match.Groups[2].Value
    if ($bytes.Length -ne $expected.Length -or
        (Get-CIWCSha256 -Bytes $bytes) -cne
            (Get-CIWCSha256 -Bytes $expected)) {
        Fail-CIWorkflowControl -Code 'CIWC_BUILD_CONTEXT_INVALID'
    }
    return [pscustomobject]@{
        Bytes = $bytes
        Revision = $match.Groups[1].Value
        CreatedUtc = $match.Groups[2].Value
        Sha256 = Get-CIWCSha256 -Bytes $bytes
    }
}

function Get-CIWCPhaseBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$ContractSha256,
        [string]$ManifestSha256Value = '',
        [string]$ArtifactSetSha256 = ''
    )
    if ($Name -cnotmatch '^(?:prepared|initialized|run-started|run-succeeded|run-failed|finalized|sealed)$' -or
        $ContractSha256 -cnotmatch '^[0-9a-f]{64}$' -or
        (-not [string]::IsNullOrEmpty($ManifestSha256Value) -and
            $ManifestSha256Value -cnotmatch '^[0-9a-f]{64}$') -or
        (-not [string]::IsNullOrEmpty($ArtifactSetSha256) -and
            $ArtifactSetSha256 -cnotmatch '^[0-9a-f]{64}$')) {
        Fail-CIWorkflowControl -Code 'CIWC_PHASE_RECORD_INVALID'
    }
    $text =
        "phase=$Name`n" +
        "contract_sha256=$ContractSha256`n" +
        "manifest_sha256=$ManifestSha256Value`n" +
        "artifact_set_sha256=$ArtifactSetSha256`n"
    return ,([byte[]]$script:Utf8NoBom.GetBytes($text))
}

function Get-CIWCExactFileBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    return ,([byte[]](Read-CIWCBoundedFile `
        -Path $Path `
        -MaximumBytes 4096 `
        -Code $Code))
}

function Assert-CIWCExactFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$ExpectedBytes,
        [Parameter(Mandatory = $true)][string]$Code
    )
    [byte[]]$actual = Get-CIWCExactFileBytes -Path $Path -Code $Code
    if ($actual.Length -ne $ExpectedBytes.Length -or
        (Get-CIWCSha256 -Bytes $actual) -cne
            (Get-CIWCSha256 -Bytes $ExpectedBytes)) {
        Fail-CIWorkflowControl -Code $Code
    }
}

function Assert-CIWCFileAbsent {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (Test-Path -LiteralPath $Path) {
        Fail-CIWorkflowControl -Code $Code
    }
}

function Write-CIWCGitHubValues {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Values
    )
    foreach ($entry in $Values.GetEnumerator()) {
        $value = [string]$entry.Value
        if ($value.IndexOfAny([char[]]@([char]10, [char]13)) -ge 0) {
            Fail-CIWorkflowControl -Code 'CIWC_GITHUB_VALUE_INVALID'
        }
        [IO.File]::AppendAllText(
            $Path,
            "$($entry.Key)=$value`n",
            $script:Utf8NoBom
        )
    }
}

function Get-CIWCLayout {
    param([Parameter(Mandatory = $true)][string]$Base)
    $control = Join-Path $Base 'control'
    $trusted = Join-Path $Base 'trusted'
    $work = Join-Path $trusted 'work'
    return [pscustomobject]@{
        Base = $Base
        Control = $control
        Trusted = $trusted
        Work = $work
        Source = Join-Path $work 'source'
        Stage = Join-Path $work 'stage'
        Artifact = Join-Path $work 'artifact'
        Cache = Join-Path $work 'cache'
        Execution = Join-Path $trusted 'execution'
        Manifest = Join-Path $work 'artifact/public-tree-manifest.v1.json'
        Controller = Join-Path $control 'release-job-controller.ps1'
        CommandRunner = Join-Path $control 'release-command-runner.ps1'
        WorkflowJob = Join-Path $control 'workflow-job.ps1'
        Self = Join-Path $control 'workflow-control.ps1'
        BuildContext = Join-Path $control 'build-context.v1'
        Contract = Join-Path $control 'job-contract.v1'
        Prepared = Join-Path $control 'phase-prepared.v1'
        Initialized = Join-Path $control 'phase-initialized.v1'
        RunStarted = Join-Path $control 'phase-run-started.v1'
        RunSucceeded = Join-Path $control 'phase-run-succeeded.v1'
        RunFailed = Join-Path $control 'phase-run-failed.v1'
        Finalized = Join-Path $control 'phase-finalized.v1'
        Sealed = Join-Path $control 'phase-sealed.v1'
    }
}

function Get-CIWCArtifactContract {
    param(
        [Parameter(Mandatory = $true)][string]$Kind,
        [string]$Goos = '',
        [string]$Goarch = '',
        [switch]$PayloadOnly
    )
    $required = New-Object 'Collections.Generic.List[string]'
    [void]$required.Add('public-tree-manifest.v1.json')
    if (-not $PayloadOnly) {
        foreach ($path in @(
            'supply-chain/sbom.spdx.json',
            'supply-chain/provenance.unsigned.v1.json',
            'supply-chain/checksums.sha256'
        )) {
            [void]$required.Add($path)
        }
    }
    $optional = New-Object 'Collections.Generic.List[string]'
    switch ($Kind) {
        'linux-quality' {
            foreach ($path in @(
                'evidence/linux-ordinary/evidence.json',
                'evidence/linux-ordinary/exit-code.txt',
                'evidence/linux-ordinary/go-test.stdout.jsonl',
                'evidence/linux-ordinary/go-test.stderr.log'
            )) {
                [void]$required.Add($path)
            }
        }
        'windows' {
            foreach ($path in @(
                'evidence/windows-ordinary/evidence.json',
                'evidence/windows-ordinary/exit-code.txt',
                'evidence/windows-ordinary/go-test.stdout.jsonl',
                'evidence/windows-ordinary/go-test.stderr.log'
            )) {
                [void]$required.Add($path)
            }
            [void]$optional.Add('bin/freeagent-windows-amd64.exe')
        }
        'linux-race' {
            foreach ($path in @(
                'evidence/linux-race/evidence.json',
                'evidence/linux-race/exit-code.txt',
                'evidence/linux-race/go-test.stdout.jsonl',
                'evidence/linux-race/go-test.stderr.log',
                'evidence/linux-race/packages.txt'
            )) {
                [void]$required.Add($path)
            }
        }
        'cross-build' {
            if ($Goos -cnotmatch '^(?:windows|linux|darwin)$' -or
                $Goarch -cnotmatch '^(?:amd64|arm64)$') {
                Fail-CIWorkflowControl -Code 'CIWC_CROSS_TARGET_INVALID'
            }
            $extension = if ($Goos -ceq 'windows') { '.exe' } else { '' }
            [void]$required.Add(
                "bin/freeagent-$Goos-$Goarch$extension"
            )
            foreach ($path in @(
                'VERSION',
                'LICENSE',
                'THIRD_PARTY_NOTICES.md',
                'INSTALL.md',
                'QUICKSTART.md',
                'KNOWN_LIMITATIONS.md',
                'RELEASE_NOTES.md',
                'VERIFY_CHECKSUMS.md',
                'config/README.md',
                'data/README.md',
                'config/current-v1.bootstrap.seed.json',
                'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE',
                'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml',
                'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md',
                'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json',
                'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/LICENSE',
                'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.yaml',
                'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md',
                'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/implementation/adapter.json',
                'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/schemas/config.schema.json'
            )) {
                [void]$required.Add($path)
            }
        }
    }
    return [pscustomobject]@{
        Required = [string[]]$required.ToArray()
        Optional = [string[]]$optional.ToArray()
    }
}

if ($script:ExactJobKinds -cnotcontains $JobKind) {
    Fail-CIWorkflowControl -Code 'CIWC_JOB_KIND_INVALID'
}
if ($JobKind -ceq 'cross-build') {
    if ($TargetGoos -cnotmatch '^(?:windows|linux|darwin)$' -or
        $TargetGoarch -cnotmatch '^(?:amd64|arm64)$') {
        Fail-CIWorkflowControl -Code 'CIWC_CROSS_TARGET_INVALID'
    }
} elseif (-not [string]::IsNullOrEmpty($TargetGoos) -or
    -not [string]::IsNullOrEmpty($TargetGoarch)) {
    Fail-CIWorkflowControl -Code 'CIWC_CROSS_TARGET_INVALID'
}
if ($PSCmdlet.ParameterSetName -in @('Finalize', 'Seal')) {
    if ($ManifestSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIWorkflowControl -Code 'CIWC_MANIFEST_PIN_INVALID'
    }
}
if ($PSCmdlet.ParameterSetName -ceq 'Seal' -and
    $ExpectedArtifactSetSha256 -cnotmatch '^[0-9a-f]{64}$') {
    Fail-CIWorkflowControl -Code 'CIWC_ARTIFACT_SET_PIN_INVALID'
}

$base = Get-CIWCAbsolutePath -Value $BaseRoot -Code 'CIWC_BASE_INVALID'
Assert-CIWCNotFileSystemRoot -Path $base -Code 'CIWC_BASE_INVALID'
Assert-CIWCNoReparseAncestry -Path $base -Code 'CIWC_BASE_INVALID'
$layout = Get-CIWCLayout -Base $base
$buildContext = $null
if ($PSCmdlet.ParameterSetName -ceq 'Prepare') {
    if ($Revision -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
        Fail-CIWorkflowControl -Code 'CIWC_REVISION_INVALID'
    }
    $createdUtc = [DateTime]::UtcNow.ToString(
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture
    )
    [byte[]]$contextBytes = Get-CIWCBuildContextBytes `
        -RevisionValue $Revision `
        -CreatedUtc $createdUtc
    $buildContext = [pscustomobject]@{
        Bytes = $contextBytes
        Revision = $Revision
        CreatedUtc = $createdUtc
        Sha256 = Get-CIWCSha256 -Bytes $contextBytes
    }
} else {
    if (-not (Test-Path -LiteralPath $layout.Base -PathType Container)) {
        Fail-CIWorkflowControl -Code 'CIWC_LAYOUT_MISSING'
    }
    $buildContext = Read-CIWCBuildContext -Path $layout.BuildContext
}
[byte[]]$jobContractBytes = Get-CIWCJobContractBytes `
    -Kind $JobKind `
    -BuildContextSha256 $buildContext.Sha256 `
    -Goos $TargetGoos `
    -Goarch $TargetGoarch
$jobContractSha256 = Get-CIWCSha256 -Bytes $jobContractBytes

if ($PSCmdlet.ParameterSetName -ceq 'Prepare') {
    $repository = Get-CIWCAbsolutePath `
        -Value $RepositoryRoot `
        -Code 'CIWC_REPOSITORY_INVALID'
    Assert-CIWCNotFileSystemRoot `
        -Path $repository `
        -Code 'CIWC_REPOSITORY_INVALID'
    Assert-CIWCNoReparseAncestry `
        -Path $repository `
        -Code 'CIWC_REPOSITORY_INVALID'
    if (-not (Test-Path -LiteralPath $layout.Control -PathType Container) -or
        -not (Test-Path -LiteralPath $layout.Self -PathType Leaf)) {
        Fail-CIWorkflowControl -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    }
    Assert-CIWCNoReparseAncestry `
        -Path $layout.Control `
        -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    Assert-CIWCNoReparseAncestry `
        -Path $layout.Self `
        -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    [string[]]$initialControlEntries = @(
        Get-ChildItem -LiteralPath $layout.Control -Force |
            ForEach-Object { $_.Name }
    )
    if ($initialControlEntries.Count -ne 1 -or
        $initialControlEntries[0] -cne 'workflow-control.ps1') {
        Fail-CIWorkflowControl -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    }
    if (Test-Path -LiteralPath $layout.Trusted) {
        Fail-CIWorkflowControl -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    }
    Assert-CIWCNoReparseAncestry `
        -Path $layout.Trusted `
        -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'
    [void][IO.Directory]::CreateDirectory($layout.Trusted)
    Assert-CIWCNoReparseAncestry `
        -Path $layout.Trusted `
        -Code 'CIWC_BOOTSTRAP_LAYOUT_INVALID'

    $controllerSource = Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/Invoke-CIReleaseJob.ps1') `
        -ExpectedSha256 $script:ControllerSha256 `
        -Code 'CIWC_CONTROLLER_AUTH_FAILED'
    $commandRunnerSource = Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/Invoke-CIReleaseCommand.ps1') `
        -ExpectedSha256 $script:CommandRunnerSha256 `
        -Code 'CIWC_COMMAND_RUNNER_AUTH_FAILED'
    $workflowJobSource = Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/Invoke-CIWorkflowJob.ps1') `
        -ExpectedSha256 $script:WorkflowJobSha256 `
        -Code 'CIWC_WORKFLOW_JOB_AUTH_FAILED'
    [void](Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/New-CIReleaseWorkspace.ps1') `
        -ExpectedSha256 $script:BootstrapSha256 `
        -Code 'CIWC_BOOTSTRAP_AUTH_FAILED')
    [void](Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/New-PublicStaging.ps1') `
        -ExpectedSha256 $script:GeneratorSha256 `
        -Code 'CIWC_GENERATOR_AUTH_FAILED')
    [void](Get-CIWCAuthenticatedScript `
        -Path (Join-Path $repository 'scripts/Test-PublicStaging.ps1') `
        -ExpectedSha256 $script:VerifierSha256 `
        -Code 'CIWC_VERIFIER_AUTH_FAILED')

    Write-CIWCSnapshot `
        -Path $layout.BuildContext `
        -Bytes ([byte[]]$buildContext.Bytes)
    Write-CIWCSnapshot `
        -Path $layout.Controller `
        -Bytes ([byte[]]$controllerSource.Bytes)
    Write-CIWCSnapshot `
        -Path $layout.CommandRunner `
        -Bytes ([byte[]]$commandRunnerSource.Bytes)
    Write-CIWCSnapshot `
        -Path $layout.WorkflowJob `
        -Bytes ([byte[]]$workflowJobSource.Bytes)
    Write-CIWCSnapshot `
        -Path $layout.Contract `
        -Bytes $jobContractBytes

    $objects = @(& $controllerSource.Block `
        -Prepare `
        -RepositoryRoot $repository `
        -Revision $Revision `
        -WorkRoot $layout.Work `
        -ExecutionTempRoot $layout.Execution `
        -BootstrapPath (
            Join-Path $repository 'scripts/New-CIReleaseWorkspace.ps1'
        ) `
        -ExpectedBootstrapSha256 $script:BootstrapSha256 `
        -ExpectedGeneratorSha256 $script:GeneratorSha256 `
        -ExpectedVerifierSha256 $script:VerifierSha256 `
        -TrustedTemp $layout.Trusted)
    $states = @($objects | Where-Object {
        $null -ne $_.PSObject.Properties['ManifestSha256'] -and
        $null -ne $_.PSObject.Properties['InitialArtifactSetSha256']
    })
    if ($states.Count -ne 1) {
        Fail-CIWorkflowControl -Code 'CIWC_PREPARE_RESULT_INVALID'
    }
    $state = $states[0]
    Write-CIWCGitHubValues `
        -Path $GitHubOutputPath `
        -Values ([ordered]@{
            base = $layout.Base
            source = $state.SourceRoot
            stage = $state.StageRoot
            artifact = $state.ArtifactRoot
            cache = $state.CacheRoot
            execution_temp = $state.ExecutionTempRoot
            manifest = $state.ManifestPath
            manifest_sha256 = $state.ManifestSha256
            control = $layout.Self
        })
    Write-CIWCGitHubValues `
        -Path $GitHubEnvironmentPath `
        -Values ([ordered]@{
            GOWORK = 'off'
            GOENV = 'off'
            GOTOOLCHAIN = 'local'
            GOFLAGS = '-mod=readonly -buildvcs=false'
            GOCACHE = Join-Path $state.CacheRoot 'build'
            GOMODCACHE = Join-Path $state.CacheRoot 'module'
            GOTMPDIR = Join-Path $state.CacheRoot 'go-tmp'
            GOPATH = Join-Path $state.CacheRoot 'gopath'
            TEMP = $state.ExecutionTempRoot
            TMP = $state.ExecutionTempRoot
            TMPDIR = $state.ExecutionTempRoot
            GIT_TERMINAL_PROMPT = '0'
            GIT_NO_REPLACE_OBJECTS = '1'
        })
    Write-CIWCSnapshot `
        -Path $layout.Prepared `
        -Bytes (Get-CIWCPhaseBytes `
            -Name 'prepared' `
            -ContractSha256 $jobContractSha256)
    Write-Host (
        'CI_WORKFLOW_CONTROL_PREPARE_PASS ' +
        "kind=$JobKind manifest_sha256=$($state.ManifestSha256)"
    )
    return
}

if (-not (Test-Path -LiteralPath $layout.Base -PathType Container)) {
    Fail-CIWorkflowControl -Code 'CIWC_LAYOUT_MISSING'
}
Assert-CIWCExactFile `
    -Path $layout.Contract `
    -ExpectedBytes $jobContractBytes `
    -Code 'CIWC_JOB_CONTRACT_MISMATCH'
[byte[]]$preparedBytes = Get-CIWCPhaseBytes `
    -Name 'prepared' `
    -ContractSha256 $jobContractSha256
Assert-CIWCExactFile `
    -Path $layout.Prepared `
    -ExpectedBytes $preparedBytes `
    -Code 'CIWC_PHASE_INVALID'

if ($PSCmdlet.ParameterSetName -in @('Initialize', 'Run')) {
    [byte[]]$initializedBytes = Get-CIWCPhaseBytes `
        -Name 'initialized' `
        -ContractSha256 $jobContractSha256
    if ($PSCmdlet.ParameterSetName -ceq 'Initialize') {
        foreach ($path in @(
            $layout.Initialized,
            $layout.RunStarted,
            $layout.RunSucceeded,
            $layout.RunFailed,
            $layout.Finalized,
            $layout.Sealed
        )) {
            Assert-CIWCFileAbsent -Path $path -Code 'CIWC_PHASE_INVALID'
        }
    } else {
        Assert-CIWCExactFile `
            -Path $layout.Initialized `
            -ExpectedBytes $initializedBytes `
            -Code 'CIWC_PHASE_INVALID'
        foreach ($path in @(
            $layout.RunStarted,
            $layout.RunSucceeded,
            $layout.RunFailed,
            $layout.Finalized,
            $layout.Sealed
        )) {
            Assert-CIWCFileAbsent -Path $path -Code 'CIWC_PHASE_INVALID'
        }
    }
    $workflowJob = Get-CIWCAuthenticatedScript `
        -Path $layout.WorkflowJob `
        -ExpectedSha256 $script:WorkflowJobSha256 `
        -Code 'CIWC_WORKFLOW_JOB_AUTH_FAILED'
    $parameters = [ordered]@{
        JobKind = $JobKind
        StageRoot = $layout.Stage
        ArtifactRoot = $layout.Artifact
        ExecutionTempRoot = $layout.Execution
    }
    if ($PSCmdlet.ParameterSetName -ceq 'Initialize') {
        $parameters.Initialize = $true
        & $workflowJob.Block @parameters
        Write-CIWCSnapshot `
            -Path $layout.Initialized `
            -Bytes $initializedBytes
        return
    } else {
        $parameters.Run = $true
        $parameters.CommandRunnerPath = $layout.CommandRunner
        $parameters.ExpectedCommandRunnerSha256 =
            $script:CommandRunnerSha256
        if (-not [string]::IsNullOrWhiteSpace($TargetGoos)) {
            $parameters.TargetGoos = $TargetGoos
        }
        if (-not [string]::IsNullOrWhiteSpace($TargetGoarch)) {
            $parameters.TargetGoarch = $TargetGoarch
        }
        if ($JobKind -ceq 'cross-build') {
            if ($buildContext.Revision -cnotmatch '^[0-9a-f]{40}$') {
                Fail-CIWorkflowControl -Code 'CIWC_REVISION_INVALID'
            }
            $parameters.Revision = $buildContext.Revision
        }
    }
    Write-CIWCSnapshot `
        -Path $layout.RunStarted `
        -Bytes (Get-CIWCPhaseBytes `
            -Name 'run-started' `
            -ContractSha256 $jobContractSha256)
    try {
        & $workflowJob.Block @parameters
    } catch {
        Write-CIWCSnapshot `
            -Path $layout.RunFailed `
            -Bytes (Get-CIWCPhaseBytes `
                -Name 'run-failed' `
                -ContractSha256 $jobContractSha256)
        throw
    }
    Write-CIWCSnapshot `
        -Path $layout.RunSucceeded `
        -Bytes (Get-CIWCPhaseBytes `
            -Name 'run-succeeded' `
            -ContractSha256 $jobContractSha256)
    return
}

$initializedBytes = Get-CIWCPhaseBytes `
    -Name 'initialized' `
    -ContractSha256 $jobContractSha256
Assert-CIWCExactFile `
    -Path $layout.Initialized `
    -ExpectedBytes $initializedBytes `
    -Code 'CIWC_PHASE_INVALID'
Assert-CIWCExactFile `
    -Path $layout.RunStarted `
    -ExpectedBytes (Get-CIWCPhaseBytes `
        -Name 'run-started' `
        -ContractSha256 $jobContractSha256) `
    -Code 'CIWC_PHASE_INVALID'
$runSucceeded = Test-Path -LiteralPath $layout.RunSucceeded -PathType Leaf
$runFailed = Test-Path -LiteralPath $layout.RunFailed -PathType Leaf
if ($runSucceeded -eq $runFailed) {
    Fail-CIWorkflowControl -Code 'CIWC_PHASE_INVALID'
}
if ($runSucceeded) {
    Assert-CIWCExactFile `
        -Path $layout.RunSucceeded `
        -ExpectedBytes (Get-CIWCPhaseBytes `
            -Name 'run-succeeded' `
            -ContractSha256 $jobContractSha256) `
        -Code 'CIWC_PHASE_INVALID'
    Assert-CIWCFileAbsent `
        -Path $layout.RunFailed `
        -Code 'CIWC_PHASE_INVALID'
} else {
    Assert-CIWCExactFile `
        -Path $layout.RunFailed `
        -ExpectedBytes (Get-CIWCPhaseBytes `
            -Name 'run-failed' `
            -ContractSha256 $jobContractSha256) `
        -Code 'CIWC_PHASE_INVALID'
    Assert-CIWCFileAbsent `
        -Path $layout.RunSucceeded `
        -Code 'CIWC_PHASE_INVALID'
    if ($JobKind -in @('permanent', 'cross-build')) {
        Fail-CIWorkflowControl -Code 'CIWC_RUN_NOT_SUCCESSFUL'
    }
}
$expectedFinalizeArtifactSetSha256 = ''
$expectedFinalizeArtifactFileCount = 0
$expectedFinalizeArtifactBytes = [int64]0
if ($PSCmdlet.ParameterSetName -ceq 'Finalize') {
    Assert-CIWCFileAbsent `
        -Path $layout.Finalized `
        -Code 'CIWC_PHASE_INVALID'
    Assert-CIWCFileAbsent `
        -Path $layout.Sealed `
        -Code 'CIWC_PHASE_INVALID'
} else {
    Assert-CIWCExactFile `
        -Path $layout.Finalized `
        -ExpectedBytes (Get-CIWCPhaseBytes `
            -Name 'finalized' `
            -ContractSha256 $jobContractSha256 `
            -ManifestSha256Value $ManifestSha256 `
            -ArtifactSetSha256 $ExpectedArtifactSetSha256) `
        -Code 'CIWC_PHASE_INVALID'
    Assert-CIWCFileAbsent `
        -Path $layout.Sealed `
        -Code 'CIWC_PHASE_INVALID'
}

$controller = Get-CIWCAuthenticatedScript `
    -Path $layout.Controller `
    -ExpectedSha256 $script:ControllerSha256 `
    -Code 'CIWC_CONTROLLER_AUTH_FAILED'
$payloadContract = Get-CIWCArtifactContract `
    -Kind $JobKind `
    -Goos $TargetGoos `
    -Goarch $TargetGoarch `
    -PayloadOnly
$contract = Get-CIWCArtifactContract `
    -Kind $JobKind `
    -Goos $TargetGoos `
    -Goarch $TargetGoarch
if ($PSCmdlet.ParameterSetName -ceq 'Finalize') {
    $payloadVerifyParameters = [ordered]@{
        Verify = $true
        SourceRoot = $layout.Source
        StageRoot = $layout.Stage
        ArtifactRoot = $layout.Artifact
        ManifestPath = $layout.Manifest
        ManifestSha256 = $ManifestSha256
        ExpectedVerifierSha256 = $script:VerifierSha256
        TrustedTemp = $layout.Execution
        RequiredArtifactRelativePath = $payloadContract.Required
        OptionalArtifactRelativePath = $payloadContract.Optional
    }
    $payloadObjects = @(& $controller.Block @payloadVerifyParameters)
    $payloadStates = @($payloadObjects | Where-Object {
        $null -ne $_.PSObject.Properties['ArtifactSetSha256']
    })
    if ($payloadStates.Count -ne 1) {
        Fail-CIWorkflowControl -Code 'CIWC_VERIFY_RESULT_INVALID'
    }
    $generationParameters = [ordered]@{
        GenerateSupplyChain = $true
        SourceRoot = $layout.Source
        ArtifactRoot = $layout.Artifact
        ModuleCacheRoot = Join-Path $layout.Cache 'module'
        TrustedTemp = $layout.Execution
        Revision = $buildContext.Revision
        CreatedUtc = $buildContext.CreatedUtc
        JobKind = $JobKind
        RunSucceeded = [bool]$runSucceeded
        MetadataGeneratorSha256 = $script:ControllerSha256
        RequiredArtifactRelativePath = $payloadContract.Required
        OptionalArtifactRelativePath = $payloadContract.Optional
    }
    if (-not [string]::IsNullOrWhiteSpace($TargetGoos)) {
        $generationParameters.TargetGoos = $TargetGoos
    }
    if (-not [string]::IsNullOrWhiteSpace($TargetGoarch)) {
        $generationParameters.TargetGoarch = $TargetGoarch
    }
    $metadataObjects = @(& $controller.Block @generationParameters)
    $metadataStates = @($metadataObjects | Where-Object {
        $null -ne $_.PSObject.Properties['InputSetSha256'] -and
        $null -ne $_.PSObject.Properties['SbomSha256'] -and
        $null -ne $_.PSObject.Properties['ProvenanceSha256'] -and
        $null -ne $_.PSObject.Properties['ChecksumsSha256'] -and
        $null -ne $_.PSObject.Properties['ExpectedArtifactSetSha256'] -and
        $null -ne $_.PSObject.Properties['ExpectedArtifactFileCount'] -and
        $null -ne $_.PSObject.Properties['ExpectedArtifactBytes']
    })
    if ($metadataStates.Count -ne 1) {
        Fail-CIWorkflowControl -Code 'CIWC_SUPPLY_CHAIN_RESULT_INVALID'
    }
    $metadataState = $metadataStates[0]
    foreach ($hashName in @(
        'InputSetSha256',
        'SbomSha256',
        'ProvenanceSha256',
        'ChecksumsSha256',
        'ExpectedArtifactSetSha256'
    )) {
        $hashValue = $metadataState.PSObject.Properties[$hashName].Value
        if (-not ($hashValue -is [string]) -or
            $hashValue -cnotmatch '^[0-9a-f]{64}$') {
            Fail-CIWorkflowControl -Code 'CIWC_SUPPLY_CHAIN_RESULT_INVALID'
        }
    }
    if (-not (
            $metadataState.ExpectedArtifactFileCount -is [int]
        ) -or [int]$metadataState.ExpectedArtifactFileCount -le 0 -or
        -not (
            $metadataState.ExpectedArtifactBytes -is [long]
        ) -or [int64]$metadataState.ExpectedArtifactBytes -le 0) {
        Fail-CIWorkflowControl -Code 'CIWC_SUPPLY_CHAIN_RESULT_INVALID'
    }
    $expectedFinalizeArtifactSetSha256 =
        [string]$metadataState.ExpectedArtifactSetSha256
    $expectedFinalizeArtifactFileCount =
        [int]$metadataState.ExpectedArtifactFileCount
    $expectedFinalizeArtifactBytes =
        [int64]$metadataState.ExpectedArtifactBytes
}
$parameters = [ordered]@{
    Verify = $true
    SourceRoot = $layout.Source
    StageRoot = $layout.Stage
    ArtifactRoot = $layout.Artifact
    ManifestPath = $layout.Manifest
    ManifestSha256 = $ManifestSha256
    ExpectedVerifierSha256 = $script:VerifierSha256
    TrustedTemp = $layout.Execution
    RequiredArtifactRelativePath = $contract.Required
    OptionalArtifactRelativePath = $contract.Optional
}
if ($PSCmdlet.ParameterSetName -ceq 'Finalize') {
    $parameters.ExpectedArtifactSetSha256 =
        $expectedFinalizeArtifactSetSha256
} elseif ($PSCmdlet.ParameterSetName -ceq 'Seal') {
    $parameters.ExpectedArtifactSetSha256 = $ExpectedArtifactSetSha256
}
$objects = @(& $controller.Block @parameters)
$states = @($objects | Where-Object {
    $null -ne $_.PSObject.Properties['ArtifactSetSha256']
})
if ($states.Count -ne 1) {
    Fail-CIWorkflowControl -Code 'CIWC_VERIFY_RESULT_INVALID'
}
$state = $states[0]
if ($PSCmdlet.ParameterSetName -ceq 'Finalize') {
    if ([int]$state.ArtifactFileCount -ne
            $expectedFinalizeArtifactFileCount -or
        [int64]$state.ArtifactBytes -ne
            $expectedFinalizeArtifactBytes) {
        Fail-CIWorkflowControl -Code 'CIWC_SUPPLY_CHAIN_RESULT_INVALID'
    }
    Write-CIWCGitHubValues `
        -Path $GitHubOutputPath `
        -Values ([ordered]@{
            artifact_set_sha256 = $state.ArtifactSetSha256
            artifact_file_count = $state.ArtifactFileCount
            artifact_bytes = $state.ArtifactBytes
            run_succeeded = if ($runSucceeded) { 'true' } else { 'false' }
        })
    Write-CIWCSnapshot `
        -Path $layout.Finalized `
        -Bytes (Get-CIWCPhaseBytes `
            -Name 'finalized' `
            -ContractSha256 $jobContractSha256 `
            -ManifestSha256Value $ManifestSha256 `
            -ArtifactSetSha256 $state.ArtifactSetSha256)
    Write-Host (
        'CI_WORKFLOW_CONTROL_FINALIZE_PASS ' +
        "kind=$JobKind artifact_set_sha256=$($state.ArtifactSetSha256)"
    )
} else {
    Write-CIWCSnapshot `
        -Path $layout.Sealed `
        -Bytes (Get-CIWCPhaseBytes `
            -Name 'sealed' `
            -ContractSha256 $jobContractSha256 `
            -ManifestSha256Value $ManifestSha256 `
            -ArtifactSetSha256 $state.ArtifactSetSha256)
    Write-Host (
        'CI_WORKFLOW_CONTROL_SEAL_PASS ' +
        "kind=$JobKind artifact_set_sha256=$($state.ArtifactSetSha256)"
    )
}
